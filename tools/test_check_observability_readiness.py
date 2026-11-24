#!/usr/bin/env python3
"""Unit tests for check_observability_readiness.py."""

from __future__ import annotations

import contextlib
import io
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parent

sys.path.insert(0, str(HERE))

import check_observability_readiness as g  # noqa: E402


class GateCase(unittest.TestCase):
    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="observability-readiness-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)

    def write(self, rel: str, text: str) -> None:
        path = pathlib.Path(self.root, rel)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def run_gate(self, root: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = g.main(["--root", root or self.root])
        return code, out.getvalue(), err.getvalue()

    def seed_anchors(self) -> None:
        """Seed the packages the four positive controls name.

        Every case runs the whole gate, so the tree has to carry one package
        per anchor or an unrelated check reports an infrastructure error. The
        seeds are themselves compliant, so a case that adds one violation
        sees only that one.
        """
        for anchor in g.LOGGER_ANCHORS:
            self.write(
                f"{anchor}/module.go",
                "package a\n\n"
                "func newThing(reg *core.Registry) {\n"
                "\t_, _ = core.Resolve[log.Logger](reg)\n"
                "}\n\n"
                "var descriptor = core.Module{\n"
                "\tRequires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},\n"
                "}\n",
            )
        for anchor in g.SENSITIVE_ANCHORS:
            self.write(
                f"{anchor}/schema.go",
                "package a\n\n"
                "const dsnKey = \"dsn\"\n\n"
                "var items = map[string]any{\n"
                "\tdsnKey: {\n"
                "\t\tSensitive: true,\n"
                "\t},\n"
                "}\n",
            )
            self.write(
                f"{anchor}/register.go",
                "package a\n\n"
                "const ModuleName = \"a\"\n\n"
                "func register(delivered log.Logger) {\n"
                "\tliteral.Register(delivered.Redaction(), ModuleName+\".credentials\", cfg.DSN)\n"
                "}\n",
            )
        for anchor in g.EGRESS_ANCHORS:
            self.write(
                f"{anchor}/client.go",
                "package a\n\n"
                "func build() {\n"
                "\tclient := egress.New(egress.Options{})\n"
                "\tclient.Transport = observability.Transport(client.Transport)\n"
                "}\n",
            )
        self.write(
            f"{g.EGRESS_EXEMPT}/client.go",
            "package a\n\nfunc build() {\n\t_ = egress.New(egress.Options{})\n}\n",
        )
        self.write(
            "pkg/observability/otel/module.go",
            'package a\n\nimport "go.opentelemetry.io/otel/trace"\n\nvar _ = trace.SpanContext{}\n',
        )


class TestCompliantTree(GateCase):
    def test_the_seeded_tree_passes(self) -> None:
        """Test that the seeded tree passes."""
        self.seed_anchors()
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)
        self.assertEqual(out, "")


class TestLoggingDependency(GateCase):
    def test_a_package_that_resolves_without_declaring_is_reported(self) -> None:
        """Test that a package that resolves without declaring is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _ = core.Resolve[log.Logger](reg)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme/module.go:4: pkg/acme:", out)
        self.assertIn("(*log.Logger)(nil)", out)

    def test_a_package_that_declares_is_not_reported(self) -> None:
        """Test that a package that declares is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _ = core.Resolve[log.Logger](reg)\n}\n\n"
            "var descriptor = core.Module{\n"
            "\tRequires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_the_optional_helper_form_is_also_a_resolution(self) -> None:
        """Test that the optional helper form is also a resolution."""
        self.seed_anchors()
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _, _ = optional[log.Logger](reg, \"the logger\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme:", out)


class TestWhitespaceBetweenCalleeAndParen(GateCase):

    def test_a_comment_in_a_logger_resolution_is_still_a_resolution(self) -> None:
        """Test that a comment in a logger resolution is still a resolution."""
        self.seed_anchors()
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _ = core.Resolve[log.Logger] /* c */ (reg)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme:", out)

    def test_a_comment_in_an_egress_open_is_still_a_call(self) -> None:
        """Test that a comment in an egress open is still a call."""
        self.seed_anchors()
        self.write(
            "pkg/acme/client.go",
            "package acme\n\nfunc build() {\n\t_ = egress.New /* c */ (egress.Options{})\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("observability.Transport", out)

    def test_a_comment_in_a_global_entry_is_still_a_call(self) -> None:
        """Test that a comment in a global entry is still a call."""
        self.seed_anchors()
        self.write(
            "pkg/acme/meter.go",
            "package acme\n\nfunc build() {\n\t_ = otel.Meter /* c */ (\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("otel.Meter", out)


class TestSensitiveRedaction(GateCase):
    def test_a_secret_item_without_a_rule_is_reported(self) -> None:
        """Test that a secret item without a rule is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme/schema.go:7: pkg/acme:", out)
        self.assertIn("'secret-key'", out)

    def test_a_registered_item_is_not_reported(self) -> None:
        """Test that a registered item is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\nconst ModuleName = \"acme\"\n\n"
            "func register(delivered log.Logger) {\n"
            "\tliteral.Register(delivered.Redaction(), ModuleName+\".secret\", cfg.Secret)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_secrets_handoff_is_not_reported(self) -> None:
        """Test that a secrets handoff is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/carrier.go",
            "package acme\n\nvar spec = Spec[Config]{\n"
            "\tSecrets: func(cfg *Config) []string { return []string{cfg.Secret} },\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_secrets_identifier_handoff_is_not_reported(self) -> None:
        """Test that a secrets identifier handoff is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/carrier.go",
            "package acme\n\nvar spec = Spec[Config]{\n\tSecrets: secrets,\n}\n\n"
            "func secrets(cfg *Config) []string { return []string{cfg.Secret} }\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_decoy_secrets_field_is_not_a_handoff(self) -> None:
        """Test that a decoy secrets field is not a handoff."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/decoy.go",
            "package acme\n\nvar settings = decoy{Secrets: true}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'secret-key'", out)

    def test_a_response_struct_flag_is_not_a_config_item(self) -> None:
        """Test that a response struct flag is not a config item."""
        self.seed_anchors()
        self.write(
            "pkg/acme/views.go",
            "package acme\n\n"
            "func viewOf(item settings.Item, value string) settingView {\n"
            "\tif item.Sensitive {\n"
            "\t\treturn settingView{\n"
            "\t\t\tKey:       item.Key,\n"
            "\t\t\tSensitive: true,\n"
            "\t\t\tValue:     maskedValue,\n"
            "\t\t}\n"
            "\t}\n"
            "\treturn settingView{Key: item.Key, Value: value}\n"
            "}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_an_item_written_as_a_typed_literal_is_a_config_item(self) -> None:
        """Test that an item written as a typed literal is a config item."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nvar items = map[string]config.Item{}\n\n"
            "func add(key string) {\n"
            "\titems[key] = config.Item{\n"
            "\t\tSensitive: true,\n"
            "\t}\n"
            "}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme", out)

    def test_a_named_path_item_is_exempt(self) -> None:
        """Test that a named path item is exempt."""
        self.seed_anchors()
        self.write(
            "pkg/authn/token/schema.go",
            "package token\n\nconst signingKeyFileItem = \"signing-key-file\"\n\n"
            "var items = map[string]any{\n\tsigningKeyFileItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_file_suffixed_key_outside_the_named_package_is_reported(self) -> None:
        """Test that a file suffixed key outside the named package is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst signingKeyFileItem = \"signing-key-file\"\n\n"
            "var items = map[string]any{\n\tsigningKeyFileItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'signing-key-file'", out)

    def test_two_items_with_one_registered_value_are_reported(self) -> None:
        """Test that two items with one registered value are reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst (\n\tfirstKey  = \"first\"\n\tsecondKey = \"second\"\n)\n\n"
            "var items = map[string]any{\n\tfirstKey: {\n\t\tSensitive: true,\n\t},\n"
            "\tsecondKey: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\nfunc register(delivered log.Logger) {\n"
            "\tliteral.Register(delivered.Redaction(), \"acme.first\", cfg.First)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'second'", out)

    def test_an_unrelated_registration_does_not_cover_an_item(self) -> None:
        """Test that an unrelated registration does not cover an item."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst (\n\tfirstKey  = \"first\"\n\tsecondKey = \"second\"\n)\n\n"
            "var items = map[string]any{\n\tfirstKey: {\n\t\tSensitive: true,\n\t},\n"
            "\tsecondKey: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\nconst ModuleName = \"acme\"\n\n"
            "func register(delivered log.Logger) {\n"
            "\tliteral.Register(delivered.Redaction(), ModuleName+\".first\", cfg.First)\n"
            "}\n\n"
            "func unrelated(rule, label, x, y any) {\n"
            "\tliteral.Register(rule, label, x, y)\n"
            "}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'second'", out)

    def test_two_items_with_two_registered_values_are_not_reported(self) -> None:
        """Test that two items with two registered values are not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst (\n\tfirstKey  = \"first\"\n\tsecondKey = \"second\"\n)\n\n"
            "var items = map[string]any{\n\tfirstKey: {\n\t\tSensitive: true,\n\t},\n"
            "\tsecondKey: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\nconst ModuleName = \"acme\"\n\n"
            "func register(delivered log.Logger) {\n"
            "\tliteral.Register(delivered.Redaction(), ModuleName+\".credentials\", cfg.First, cfg.Second)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_registration_named_only_in_a_comment_is_reported(self) -> None:
        """Test that a registration named only in a comment is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\n"
            "// literal.Register(delivered.Redaction(), \"acme.secret\", cfg.Secret)\n"
            "func register() {}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'secret-key'", out)

    def test_a_registration_named_only_in_a_block_comment_or_raw_string_is_reported(self) -> None:
        """Test that a registration named only in a block comment or raw string is
        reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\n"
            "/* literal.Register(delivered.Redaction(), \"acme.secret\", cfg.Secret) */\n"
            "var note = `literal.Register(delivered.Redaction(), \"acme.secret\", cfg.Secret)`\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'secret-key'", out)

    def test_a_registration_named_only_in_a_string_is_reported(self) -> None:
        """Test that a registration named only in a string is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{\n\tsecretKeyItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        self.write(
            "pkg/acme/register.go",
            "package acme\n\n"
            "var note = \"literal.Register(delivered.Redaction(), \\\"acme.secret\\\", cfg.Secret)\"\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'secret-key'", out)

    def test_a_key_on_the_same_line_as_the_flag_is_named(self) -> None:
        """Test that a key on the same line as the flag is named."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\nconst secretKeyItem = \"secret-key\"\n\n"
            "var items = map[string]any{secretKeyItem: {Sensitive: true}}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'secret-key'", out)
        self.assertNotIn("''", out)

    def test_two_items_on_one_line_are_each_named(self) -> None:
        """Test that two items on one line are each named."""
        self.seed_anchors()
        self.write(
            "pkg/acme/schema.go",
            "package acme\n\n"
            "var items = map[string]any{first: {Sensitive: true}, second: {Sensitive: true}}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("'first'", out)
        self.assertIn("'second'", out)

    def test_a_shared_unit_mounted_by_a_registrar_is_not_reported(self) -> None:
        """Test that a shared unit mounted by a registrar is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/internal/pool/pool.go",
            "package pool\n\nconst dsnKey = \"dsn\"\n\n"
            "var items = map[string]any{\n\tdsnKey: {\n\t\tSensitive: true,\n\t},\n}\n\n"
            "func Items() map[string]any { return items }\n",
        )
        self.write(
            "pkg/db/postgres/module.go",
            "package postgres\n\nvar spec = Spec{\n"
            "\tItems:   pool.Items(),\n"
            "\tSecrets: func(cfg *pool.Config) []string { return []string{cfg.DSN} },\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_shared_unit_with_no_registrar_is_reported(self) -> None:
        """Test that a shared unit with no registrar is reported."""
        self.seed_anchors()
        self.write(
            "pkg/internal/pool/pool.go",
            "package pool\n\nconst dsnKey = \"dsn\"\n\n"
            "var items = map[string]any{\n\tdsnKey: {\n\t\tSensitive: true,\n\t},\n}\n\n"
            "func Items() map[string]any { return items }\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/internal/pool:", out)


class TestEgressTransport(GateCase):
    def test_a_client_without_the_wrapper_is_reported(self) -> None:
        """Test that a client without the wrapper is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/client.go",
            "package acme\n\nfunc build() {\n\t_ = egress.New(egress.Options{})\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme/client.go:4: pkg/acme:", out)
        self.assertIn("observability.Transport", out)

    def test_a_wrapped_client_is_not_reported(self) -> None:
        """Test that a wrapped client is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/client.go",
            "package acme\n\nfunc build() {\n"
            "\tclient := egress.New(egress.Options{})\n"
            "\tclient.Transport = observability.Transport(client.Transport)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_the_wechat_exception_is_not_reported(self) -> None:
        """Test that the wechat exception is not reported."""
        self.seed_anchors()
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)
        self.assertNotIn(g.EGRESS_EXEMPT, out)


class TestNoGlobalProvider(GateCase):
    def test_a_global_entry_is_reported(self) -> None:
        """Test that a global entry is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/meter.go",
            "package acme\n\nfunc build() {\n\t_ = otel.Meter(\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("pkg/acme/meter.go:4: pkg/acme:", out)
        self.assertIn("otel.Meter", out)

    def test_a_provider_meter_is_not_reported(self) -> None:
        """Test that a provider meter is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/meter.go",
            "package acme\n\nfunc build(provider observability.MeterProvider) {\n"
            "\t_ = provider.Meter(\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_installing_the_global_provider_is_reported(self) -> None:
        """Test that installing the global provider is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/provider.go",
            "package acme\n\nfunc install(p trace.TracerProvider) {\n\totel.SetTracerProvider(p)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("otel.SetTracerProvider", out)

    def test_a_handle_on_the_global_provider_is_reported(self) -> None:
        """Test that a handle on the global provider is reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/handle.go",
            "package acme\n\nfunc report(err error) {\n\totel.Handle(err)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("otel.Handle", out)

    def test_a_global_entry_in_a_test_file_is_not_reported(self) -> None:
        """Test that a global entry in a test file is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/provider_test.go",
            "package acme\n\nfunc TestProvider(t *testing.T) {\n\t_ = otel.Meter(\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_global_entry_named_only_in_a_comment_is_not_reported(self) -> None:
        """Test that a global entry named only in a comment is not reported."""
        self.seed_anchors()
        self.write(
            "pkg/acme/provider.go",
            "package acme\n\n// otel.Meter(\"acme\") would reach the global no-op\nfunc install() {}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)


class TestPositiveControls(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self) -> None:
        """Test that a root that is not a directory is an infrastructure error."""
        missing = str(pathlib.Path(self.root, "absent"))
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = g.main(["--root", missing])
        self.assertEqual(code, 2)
        self.assertIn("not a directory", err.getvalue())

    def test_a_tree_with_no_go_file_is_an_infrastructure_error(self) -> None:
        """Test that a tree with no go file is an infrastructure error."""
        self.write("pkg/README.md", "no go here\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("no Go file", err)

    def test_a_scan_that_drew_an_empty_set_is_an_infrastructure_error(self) -> None:
        """Test that a scan that drew an empty set is an infrastructure error."""
        self.write("pkg/acme/empty.go", "package acme\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("empty set", err)

    def test_a_scan_that_lost_its_anchor_is_an_infrastructure_error(self) -> None:
        """Test that a scan that lost its anchor is an infrastructure error."""
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _ = core.Resolve[log.Logger](reg)\n}\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("anchors", err)

    def test_a_stale_egress_exemption_is_an_infrastructure_error(self) -> None:
        """Test that a stale egress exemption is an infrastructure error."""
        self.seed_anchors()
        pathlib.Path(self.root, g.EGRESS_EXEMPT, "client.go").unlink()
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("allow-listed", err)


class TestTheRepositoryItself(GateCase):
    def test_the_repository_reports_no_finding(self) -> None:
        """Test that the repository reports no finding."""
        code, out, err = self.run_gate(str(REPO))
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertEqual(out, "")


if __name__ == "__main__":
    unittest.main()
