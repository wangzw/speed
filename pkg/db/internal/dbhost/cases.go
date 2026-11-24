package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// scenarioEnv names the scenario to run. It is read from the environment because
// the configuration loader rejects positional arguments, and it stays outside the
// host's prefix so it is not reported as an input item nobody reads.
const scenarioEnv = "SPEED_DB_E2E_CASE"

const (
	caseFirstMigration        = "first-migration"
	caseSerializerRoundTrip   = "serializer-round-trip"
	caseOneEngineConfigured   = "one-engine-configured"
	caseBothEnginesConfigured = "both-engines-configured"
	caseNoEngineConfigured    = "no-engine-configured"
	caseMigrateSubcommand     = "migrate-subcommand"
	caseConnectRefused        = "connect-refused"
)

const (
	roleEnv       = "SPEED_DB_E2E_ROLE"
	rendezvousEnv = "SPEED_DB_E2E_RENDEZVOUS"
)

const probeModuleName = "probe"

// The markers the concurrency case reads, each with the moment it was reached:
// that is how a run whose replicas overlapped is told from one where the second
// arrived after the first had finished.
const (
	markStart   = "start"
	markApplied = "applied"
	markNoop    = "noop"
)

const roundTripMarker = "dbhost: round-trip ok"

const (
	recordedMarker = "dbhost: recorded"
	// servedMarker's absence is what says the migrate command stopped at the
	// migration stage: a run that went further leaves the same status and the
	// same record table.
	servedMarker = "dbhost: served"
)

// recordTableName is the name the database module keeps to itself, spelled here
// by the scenario that reads the table back.
const recordTableName = "db_migrations"

const appliersTable = "dbhost_appliers"

const (
	replicas          = 2
	rendezvousTimeout = 60 * time.Second
)

// migrationFiles holds one subdirectory per dialect, the shape the Migrations
// resource asks for.
//
//go:embed migrations
var migrationFiles embed.FS

// declaredMigrations reaches the migrations subdirectory rather than handing the
// whole FS: the module reads the set relative to its root, so an FS rooted one
// level too high would declare no migrations at all.
func declaredMigrations() db.Migrations {
	set, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("dbhost: the embedded migrations are not where this file puts them: " + err.Error())
	}
	return db.Migrations{FS: set}
}

type probe struct {
	capability db.Database
	identity   string
}

func probeNew(_ context.Context, reg *core.Registry) (any, error) {
	capability, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, err
	}
	p := &probe{capability: capability}
	switch scenario() {
	case caseFirstMigration:
		if err := p.joinReplicas(); err != nil {
			return nil, err
		}
	case caseSerializerRoundTrip:
		if err := p.encryptedRoundTrip(); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func probeMigrate(_ context.Context, _ *core.Registry, instance any) error {
	p, ok := instance.(*probe)
	if !ok || p == nil {
		return nil
	}
	switch scenario() {
	case caseFirstMigration:
		return p.reportWhoApplied()
	case caseMigrateSubcommand:
		return p.reportRecordedMigrations()
	}
	return nil
}

// reportRecordedMigrations prints the migration record table, one line per row,
// naming module and file so the case can tell this host's own declaration from
// some other migration.
func (p *probe) reportRecordedMigrations() error {
	var rows []struct {
		Module string
		File   string
	}
	query := `SELECT module, file FROM ` + recordTableName + ` ORDER BY module, file`
	if err := p.capability.DB().Raw(query).Scan(&rows).Error; err != nil {
		return fmt.Errorf("reading the migration record table: %w", err)
	}
	for _, row := range rows {
		say(recordedMarker + "\t" + row.Module + "\t" + row.File)
	}
	return nil
}

// joinReplicas meets the other replica at the rendezvous and marks the moment
// this process has all but reached its migration stage. Construction is the last
// point of a run this host controls, so the marker precedes the migration.
func (p *probe) joinReplicas() error {
	identity, err := p.sessionIdentity()
	if err != nil {
		return err
	}
	p.identity = identity
	role := os.Getenv(roleEnv)
	switch {
	case role == "":
		return fmt.Errorf("%s is not set, and this scenario needs the name this replica goes by", roleEnv)
	case identity == "":
		return fmt.Errorf("the locator gives this session no application_name, so the migration cannot " +
			"record which replica ran it and the two would be indistinguishable afterwards; give every " +
			"replica a locator that names it")
	case identity != role:
		return fmt.Errorf("%s calls this replica %q and the locator names its sessions %q: the migration "+
			"records the session that ran it, so the two have to agree", roleEnv, role, identity)
	}
	if err := meetOtherReplicas(role); err != nil {
		return err
	}
	mark(markStart)
	return nil
}

// sessionIdentity reads the name this replica's connections go by: the session's
// own answer, which is what the migration records.
func (p *probe) sessionIdentity() (string, error) {
	var identity string
	if err := p.capability.DB().Raw(`SELECT current_setting('application_name')`).Scan(&identity).Error; err != nil {
		return "", fmt.Errorf("reading this session's application_name: %w", err)
	}
	return identity, nil
}

// reportWhoApplied prints the marker the concurrency case divides the replicas
// by. The row the migration wrote is the whole of the evidence: everything else
// a replica could observe after the fact is identical on both sides.
func (p *probe) reportWhoApplied() error {
	var appliers []string
	if err := p.capability.DB().Raw(`SELECT role FROM ` + appliersTable).Scan(&appliers).Error; err != nil {
		return fmt.Errorf("reading which session ran the migration: %w", err)
	}
	switch {
	case len(appliers) == 0:
		return fmt.Errorf("the migration left no record of the session that ran it, so this run cannot " +
			"say which replica applied it")
	case len(appliers) > 1:
		return fmt.Errorf("%d sessions ran the migration: %s", len(appliers), strings.Join(appliers, ", "))
	case appliers[0] == p.identity:
		mark(markApplied)
	default:
		mark(markNoop)
	}
	return nil
}

const roundTripPlaintext = "dbhost-e2e-plaintext"

type secret struct {
	ID    uint
	Value string `gorm:"serializer:encrypted"`
}

// encryptedRoundTrip writes one model through the delivered handle and reads it
// back. The process is fresh, so a serializer registered after the handle was
// delivered is unresolved here, on the first statement that carries it.
func (p *probe) encryptedRoundTrip() error {
	handle := p.capability.DB()
	if err := handle.AutoMigrate(&secret{}); err != nil {
		return fmt.Errorf("creating the table the encrypted field lives in: %w", err)
	}
	if err := handle.Create(&secret{Value: roundTripPlaintext}).Error; err != nil {
		return fmt.Errorf("writing the encrypted field: %w", err)
	}
	var read secret
	if err := handle.First(&read, 1).Error; err != nil {
		return fmt.Errorf("reading the encrypted field back: %w", err)
	}
	if read.Value != roundTripPlaintext {
		return fmt.Errorf("the encrypted field came back as %q, and this run wrote %q",
			read.Value, roundTripPlaintext)
	}
	say(roundTripMarker)
	return nil
}

// meetOtherReplicas blocks until every replica has reached this point, so they
// go for the migration mutex together.
func meetOtherReplicas(role string) error {
	dir := os.Getenv(rendezvousEnv)
	if dir == "" {
		return fmt.Errorf("%s is not set, and this scenario needs the directory the replicas meet in",
			rendezvousEnv)
	}
	// Opened as a root rather than joined into a path, so a role that tried to
	// leave the directory is refused.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the rendezvous directory %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	announcement, err := root.Create("ready-" + role)
	if err != nil {
		return fmt.Errorf("announcing this replica at the rendezvous: %w", err)
	}
	if err := announcement.Close(); err != nil {
		return fmt.Errorf("closing this replica's announcement: %w", err)
	}
	deadline := time.Now().Add(rendezvousTimeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("reading the rendezvous: %w", err)
		}
		arrived := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "ready-") {
				arrived++
			}
		}
		if arrived >= replicas {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("only some of the %d replicas reached the rendezvous within %s",
		replicas, rendezvousTimeout)
}

func scenario() string { return os.Getenv(scenarioEnv) }

// mark prints a marker and the moment this process reached it, which is what the
// overlap assertion reads.
func mark(name string) {
	say(name + "\t" + time.Now().UTC().Format(time.RFC3339Nano))
}
