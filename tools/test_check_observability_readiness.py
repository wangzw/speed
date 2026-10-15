#!/usr/bin/env python3
"""Unit tests for check_observability_readiness.py.

Stdlib-only (unittest + tempfile), matching this directory's own "plain
executables with no third-party dependencies" convention. Run directly:

    python3 tools/test_check_observability_readiness.py

Each check is driven over a constructed tree: a violation is written and the
check must report it, a compliant sibling must not, and the positive control
must fire on a tree that holds none of the call sites it is about. The trees
are built on disk rather than in memory because the gate walks a directory,
and every case seeds the anchor packages the positive controls name, so a
case that adds one violation to an otherwise clean tree sees exactly it.
"""

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
        # The positive control for check 4 is that the tree reaches upstream
        # otel at all, so the absence of the global entry means something.
        self.write(
            "pkg/observability/otel/module.go",
            'package a\n\nimport "go.opentelemetry.io/otel/trace"\n\nvar _ = trace.SpanContext{}\n',
        )


class TestCompliantTree(GateCase):
    def test_the_seeded_tree_passes(self) -> None:
        self.seed_anchors()
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)
        self.assertEqual(out, "")


class TestLoggingDependency(GateCase):
    def test_a_package_that_resolves_without_declaring_is_reported(self) -> None:
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
        # `optional[log.Logger](reg, ...)` is the package-local take-or-absent
        # helper; it is a resolution exactly as core.Resolve is.
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
    # The masking replaces a comment with spaces, so a comment between the
    # callee and its `(` leaves a gap. Each presence pattern tolerates that gap
    # (`\\s*`), so the call is still recognized; a pattern that required the
    # callee and `(` to be adjacent missed every one of these.

    def test_a_comment_in_a_logger_resolution_is_still_a_resolution(self) -> None:
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
        self.seed_anchors()
        self.write(
            "pkg/acme/client.go",
            "package acme\n\nfunc build() {\n\t_ = egress.New /* c */ (egress.Options{})\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("observability.Transport", out)

    def test_a_comment_in_a_global_entry_is_still_a_call(self) -> None:
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
        # A carrier that names its secret in a Secrets hook has handed the
        # value to the factory, which calls literal.Register.
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
        # `Secrets: secrets` hands an identifier over; it is a handoff when the
        # package declares that name as a `[]string`-returning function.
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
        # A field merely *named* Secrets -- `decoy{Secrets: true}` -- hands no
        # value to the factory. It must not be read as the `[]string` hook that
        # covers every Sensitive item: the item with no rule is still reported.
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

    def test_a_named_path_item_is_exempt(self) -> None:
        # The value is a filesystem path, not the secret. The exemption names
        # the package and the key, so the item in its own package is exempt.
        self.seed_anchors()
        self.write(
            "pkg/authn/token/schema.go",
            "package token\n\nconst signingKeyFileItem = \"signing-key-file\"\n\n"
            "var items = map[string]any{\n\tsigningKeyFileItem: {\n\t\tSensitive: true,\n\t},\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_file_suffixed_key_outside_the_named_package_is_reported(self) -> None:
        # A key that merely ends in `-file` is not exempt on its own: only the
        # two named (package, key) pairs are, because a later key of that
        # shape could still hold the secret.
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
        # Two Sensitive items, one fixed-value registration: the count falls a
        # value short, so the item past it is reported. This is the per-item
        # gap a package-level reading reports as nothing.
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
        # Two Sensitive items, one real (module-prefixed) registration, plus an
        # unrelated multi-value literal.Register whose label is some other
        # expression. The count is attributed to the items the package is being
        # judged on, so the unrelated call is not summed in and cannot mask the
        # item that has no rule: 'second' is still reported. (An earlier count
        # summed every literal.Register in the package, which this input would
        # have carried to 3 values and passed.)
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
        # A call named in a line comment is prose, not a registration: the
        # value it "hands over" is not a value this package registers.
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
        # A call quoted inside a string literal is data, not a call site.
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
        # The map opener, the item entry and the flag share one line. The key
        # is still read, so the report names the item and not an empty string.
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
        # Two entries share a line; each flag is attributed to its own key, not
        # to the first entry on the line.
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
        # A shared config unit has no module of its own; the module that
        # mounts its Items() registers the values.
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
        # The allow-listed package is part of the seed and is left unwrapped;
        # it must not be a finding.
        self.seed_anchors()
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)
        self.assertNotIn(g.EGRESS_EXEMPT, out)


class TestNoGlobalProvider(GateCase):
    def test_a_global_entry_is_reported(self) -> None:
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
        # Taking a meter off a provider that came from the registry is the
        # allowed path; only the package-level entry is forbidden.
        self.seed_anchors()
        self.write(
            "pkg/acme/meter.go",
            "package acme\n\nfunc build(provider observability.MeterProvider) {\n"
            "\t_ = provider.Meter(\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_installing_the_global_provider_is_reported(self) -> None:
        # The ADR forbids installing the global provider, and the installing
        # entry is SetTracerProvider, not Tracer.
        self.seed_anchors()
        self.write(
            "pkg/acme/provider.go",
            "package acme\n\nfunc install(p trace.TracerProvider) {\n\totel.SetTracerProvider(p)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("otel.SetTracerProvider", out)

    def test_a_handle_on_the_global_provider_is_reported(self) -> None:
        self.seed_anchors()
        self.write(
            "pkg/acme/handle.go",
            "package acme\n\nfunc report(err error) {\n\totel.Handle(err)\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 1)
        self.assertIn("otel.Handle", out)

    def test_a_global_entry_in_a_test_file_is_not_reported(self) -> None:
        # Test files are out of scope, the same as the other checks: an
        # assertion may name a call that production code must not make.
        self.seed_anchors()
        self.write(
            "pkg/acme/provider_test.go",
            "package acme\n\nfunc TestProvider(t *testing.T) {\n\t_ = otel.Meter(\"acme\")\n}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)

    def test_a_global_entry_named_only_in_a_comment_is_not_reported(self) -> None:
        self.seed_anchors()
        self.write(
            "pkg/acme/provider.go",
            "package acme\n\n// otel.Meter(\"acme\") would reach the global no-op\nfunc install() {}\n",
        )
        code, out, _ = self.run_gate()
        self.assertEqual(code, 0, out)


class TestPositiveControls(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self) -> None:
        missing = str(pathlib.Path(self.root, "absent"))
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = g.main(["--root", missing])
        self.assertEqual(code, 2)
        self.assertIn("not a directory", err.getvalue())

    def test_a_tree_with_no_go_file_is_an_infrastructure_error(self) -> None:
        self.write("pkg/README.md", "no go here\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("no Go file", err)

    def test_a_scan_that_drew_an_empty_set_is_an_infrastructure_error(self) -> None:
        # Nothing resolves a logger or marks an item sensitive, so no check
        # has anything to assert: an empty subject is not a pass.
        self.write("pkg/acme/empty.go", "package acme\n")
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("empty set", err)

    def test_a_scan_that_lost_its_anchor_is_an_infrastructure_error(self) -> None:
        # Call sites that resolve a logger exist, but none of them is in a
        # package the control names, which is what a pattern that stopped
        # matching the tree looks like.
        self.write(
            "pkg/acme/module.go",
            "package acme\n\nfunc take(reg *core.Registry) {\n"
            "\t_, _ = core.Resolve[log.Logger](reg)\n}\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("anchors", err)

    def test_a_stale_egress_exemption_is_an_infrastructure_error(self) -> None:
        # The allow-listed package did not open a client: the exemption no
        # longer names anything and could be hiding a violation.
        self.seed_anchors()
        pathlib.Path(self.root, g.EGRESS_EXEMPT, "client.go").unlink()
        code, _, err = self.run_gate()
        self.assertEqual(code, 2)
        self.assertIn("allow-listed", err)


class TestTheRepositoryItself(GateCase):
    def test_the_repository_reports_no_finding(self) -> None:
        # The gate runs over the tree it lives in and reports nothing: the
        # module the logging requirement was missing from is fixed, and a
        # package that stays off the hand-written lists still has to pass.
        code, out, err = self.run_gate(str(REPO))
        self.assertEqual(code, 0, f"{out}{err}")
        self.assertEqual(out, "")


if __name__ == "__main__":
    unittest.main()
