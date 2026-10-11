package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// pipelineModuleName is the name the pipeline module is registered under.
const pipelineModuleName = "pipeline"

// stageTable is the table the stage model maps to.
const stageTable = "pipeline_stages"

// The pipeline routes, on the API endpoint and so inside the tenant the request
// names. Any member reads the stages; only an owner or an admin of the tenant
// defines them.
const (
	listStagesRoute  = "GET /pipeline/stages"
	createStageRoute = "POST /pipeline/stages"
	patchStageRoute  = "PATCH /pipeline/stages/:key"
)

// The three kinds of stage. A deal in an open stage is being worked; one in a
// won or lost stage is closed, and moving it there stamps when it closed. The
// migration's CHECK holds the same three.
const (
	stageOpen = "open"
	stageWon  = "won"
	stageLost = "lost"
)

// stageKinds lists them for validation and for the error text that names them.
var stageKinds = []string{stageOpen, stageWon, stageLost}

// stageKeyPattern is the shape of a stage key: lower-case letters, digits and
// dashes, starting with a letter. A key is what a deal records and a client
// writes into a URL, so it is kept to characters neither has to escape.
var stageKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

var (
	errBadStageKey     = errors.New("the stage key is not lower-case letters, digits and dashes starting with a letter, at most 40 long")
	errNoStageName     = errors.New("the stage name is empty")
	errBadPosition     = errors.New("the stage position is negative")
	errBadProbability  = errors.New("the stage probability is not between 0 and 100")
	errNoProbability   = errors.New("the stage probability is missing")
	errUnknownKind     = errors.New("the stage kind is not one of " + strings.Join(stageKinds, ", "))
	errStageDiffers    = errors.New("a stage with this key exists with other attributes")
	errNotAdministered = errors.New("only an owner or an admin of this tenant defines the pipeline")
)

//go:embed openapi/pipeline.json
var pipelineOpenAPI []byte

//go:embed migrations/pipeline
var pipelineMigrationRoot embed.FS

var pipelineMigrations = subtree(pipelineMigrationRoot, "migrations/pipeline")

// PipelineStage is one stage of a tenant's pipeline. It is a scoped model: two
// tenants have stages of the same key, each its own, and the statements this
// module issues name no tenant.
type PipelineStage struct {
	tenant.Model
	Key         string    `gorm:"column:key;primaryKey"`
	Name        string    `gorm:"column:name;not null"`
	Position    int       `gorm:"column:position;not null"`
	Probability int       `gorm:"column:probability;not null"`
	Kind        string    `gorm:"column:kind;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

// TableName fixes the table this model maps to.
func (PipelineStage) TableName() string { return stageTable }

// closes reports whether moving a deal into the stage closes it.
func (s PipelineStage) closes() bool { return s.Kind == stageWon || s.Kind == stageLost }

// stageByKey finds one of the request's tenant's stages. It reads with Find
// rather than First: a key nobody defined is an ordinary answer here.
func stageByKey(ctx context.Context, database db.Database, key string) (PipelineStage, bool, error) {
	var found []PipelineStage
	err := database.DB().WithContext(ctx).Where("key = ?", key).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return PipelineStage{}, false, err
	}
	return found[0], true, nil
}

// canAdminister reports whether a role defines the pipeline.
func canAdminister(role string) bool { return roleRank(role) >= roleRank(roleAdmin) }

// pipelineTable is the capability the pipeline module stands for, so that a
// module whose tables reference the stages table can declare a requirement on
// it and have its migration applied after this one. It is a token only, and it
// is claimed exclusively so a requirement on it never brings this module into a
// command.
type pipelineTable interface{ pipelineTable() }

// pipelineProduct is the pipeline module's product.
type pipelineProduct struct {
	released
	db db.Database
}

// pipelineTable marks the product as the pipeline module's.
func (*pipelineProduct) pipelineTable() {}

// stageView is one stage as the API shows it.
type stageView struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Position    int    `json:"position"`
	Probability int    `json:"probability"`
	Kind        string `json:"kind"`
}

func viewOfStage(s PipelineStage) stageView {
	return stageView{Key: s.Key, Name: s.Name, Position: s.Position, Probability: s.Probability, Kind: s.Kind}
}

// stageList is what the listing answers with.
type stageList struct {
	Stages []stageView `json:"stages"`
}

// listStages answers the tenant's stages in pipeline order. The query carries no
// tenant condition; the filter puts it there.
func (p *pipelineProduct) listStages(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := memberOf(c, p.db, listStagesRoute); !ok {
		return
	}
	var stages []PipelineStage
	if err := p.db.DB().WithContext(ctx).Order("position, key").Find(&stages).Error; err != nil {
		log.FromContext(ctx).Error("reading the pipeline", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the pipeline cannot be read")
		return
	}
	out := stageList{Stages: make([]stageView, 0, len(stages))}
	for _, s := range stages {
		out.Stages = append(out.Stages, viewOfStage(s))
	}
	c.JSON(nethttp.StatusOK, out)
}

// stageRequest is the body of a stage definition.
type stageRequest struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Position    int    `json:"position"`
	Probability *int   `json:"probability"`
	Kind        string `json:"kind"`
}

// Validate refuses a stage whose key, name, position, probability or kind is
// not one this host accepts.
func (s stageRequest) Validate() error {
	switch {
	case !stageKeyPattern.MatchString(s.Key):
		return errBadStageKey
	case strings.TrimSpace(s.Name) == "":
		return errNoStageName
	case s.Position < 0:
		return errBadPosition
	case s.Probability == nil:
		return errNoProbability
	case *s.Probability < 0 || *s.Probability > 100:
		return errBadProbability
	case !slices.Contains(stageKinds, s.Kind):
		return errUnknownKind
	}
	return nil
}

// createStage defines a stage of the tenant's pipeline, or finds it already
// defined.
//
// The stage is found or created by its key in one statement chain: the look-up
// is scoped to the request's tenant, and on a miss the row is created with the
// key the look-up named and the attributes the request gives, the tenant half
// of the key filled by the filter. A stage the tenant already has with the same
// attributes answers as it is, so defining a pipeline twice is harmless; one
// with other attributes is a conflict, since a definition is not how a stage is
// changed.
func (p *pipelineProduct) createStage(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, p.db, createStageRoute)
	if !ok {
		return
	}
	if !canAdminister(self.Role) {
		writeError(c, nethttp.StatusForbidden, errNotAdministered.Error())
		return
	}
	var body stageRequest
	if !bindBody(c, &body) {
		return
	}
	want := PipelineStage{
		Name:        strings.TrimSpace(body.Name),
		Position:    body.Position,
		Probability: *body.Probability,
		Kind:        body.Kind,
	}
	var stage PipelineStage
	result := p.db.DB().WithContext(ctx).Where(PipelineStage{Key: body.Key}).Attrs(want).FirstOrCreate(&stage)
	if result.Error != nil {
		log.FromContext(ctx).Error("defining a stage", "error", result.Error)
		writeError(c, nethttp.StatusInternalServerError, "the stage could not be written")
		return
	}
	switch {
	case result.RowsAffected == 1:
		c.JSON(nethttp.StatusCreated, viewOfStage(stage))
	case stage.Name == want.Name && stage.Position == want.Position &&
		stage.Probability == want.Probability && stage.Kind == want.Kind:
		c.JSON(nethttp.StatusOK, viewOfStage(stage))
	default:
		writeError(c, nethttp.StatusConflict, errStageDiffers.Error())
	}
}

// stagePatch is the body of a change to a stage's probability, the one
// attribute a stage changes in place: its key is what deals record, and its
// kind decides what moving a deal into it means.
type stagePatch struct {
	Probability *int `json:"probability"`
}

// Validate refuses a change that carries no probability or one out of range.
func (s stagePatch) Validate() error {
	switch {
	case s.Probability == nil:
		return errNoProbability
	case *s.Probability < 0 || *s.Probability > 100:
		return errBadProbability
	}
	return nil
}

// patchStage changes the probability of one of the tenant's stages.
//
// The stage is addressed by the key half of its primary key; the tenant half is
// the filter's to fill from the request, as the condition it adds is.
func (p *pipelineProduct) patchStage(c *gin.Context) {
	ctx := c.Request.Context()
	self, ok := memberOf(c, p.db, patchStageRoute)
	if !ok {
		return
	}
	if !canAdminister(self.Role) {
		writeError(c, nethttp.StatusForbidden, errNotAdministered.Error())
		return
	}
	var body stagePatch
	if !bindBody(c, &body) {
		return
	}
	key := c.Param("key")
	result := p.db.DB().WithContext(ctx).Model(&PipelineStage{Key: key}).Update("probability", *body.Probability)
	if result.Error != nil {
		log.FromContext(ctx).Error("changing a stage", "error", result.Error)
		writeError(c, nethttp.StatusInternalServerError, "the stage could not be written")
		return
	}
	if result.RowsAffected == 0 {
		writeError(c, nethttp.StatusNotFound, "the pipeline has no stage with this key")
		return
	}
	stage, found, err := stageByKey(ctx, p.db, key)
	if err != nil || !found {
		log.FromContext(ctx).Error("reading a stage back", "error", err, "found", found)
		writeError(c, nethttp.StatusServiceUnavailable, "the stage cannot be read")
		return
	}
	c.JSON(nethttp.StatusOK, viewOfStage(stage))
}

// init registers the pipeline module. It runs on serve: its routes sit on the
// API endpoint.
func init() {
	registerApplication(application{
		module:     pipelineModule(),
		runsOn:     []string{serveCommand},
		models:     []any{PipelineStage{}},
		journalled: []string{createStageRoute, patchStageRoute},
	})
}

// pipelineModule declares the stages table and binds the pipeline routes.
//
// It requires the roster for the reason every module issuing statements on a
// scoped model does: a command that ran it without the roster would run its
// statements unscoped, and the requirement turns that into a startup failure.
func pipelineModule() core.Module {
	return core.Module{
		Name: pipelineModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*pipelineTable)(nil), Exclusive: true},
		},
		Resources: []any{
			db.Migrations{FS: pipelineMigrations},
			speedhttp.Spec{Endpoint: apiEndpoint, Document: pipelineOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, pipelineModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return &pipelineProduct{released: released{log: logger}, db: database}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*pipelineProduct)
			if !ok {
				return errForeignInstance(pipelineModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return fmt.Errorf("binding the pipeline routes: %w", err)
			}
			mount(api, listStagesRoute, product.listStages)
			mount(api, createStageRoute, product.createStage)
			mount(api, patchStageRoute, product.patchStage)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
