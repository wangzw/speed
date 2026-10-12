package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// activitiesModuleName is the name the activities module is registered under.
const activitiesModuleName = "activities"

// activityTable is the table the activity model maps to.
const activityTable = "activities"

// The activity routes, on the API endpoint. Any member reads a deal's timeline;
// recording and deleting an activity take the write and delete permissions. Each
// route declares the permission the authorization middleware decides it on, from
// the module's own Init; the handlers trust that decision and run no role
// comparison of their own.
const (
	createActivityRoute = "POST /activities"
	deleteActivityRoute = "DELETE /activities/:id"
	dealTimelineRoute   = "GET /deals/:id/timeline"
)

// activityPermissions is each route's required permission, in the form the
// declaration surface takes. A route the host mounts on the API endpoint must
// appear here or be declared open, or the authorization middleware refuses it as
// undeclared.
var activityPermissions = []struct {
	route string
	perm  authz.Permission
}{
	{createActivityRoute, permActivityWrite},
	{deleteActivityRoute, permActivityDel},
	{dealTimelineRoute, permActivityRead},
}

// declareActivityRoutes declares each activity route's required permission on the
// route-declaration surface, so the enforcement middleware decides the route
// rather than the handler reading a role.
//
// The surface is taken up optionally: a command that runs the activities module
// without the enforcement layer has none to declare to, and its routes there
// carry no requirement. A command that runs the layer holds every declaration to
// the permission catalog and to the process's translation once the Init window
// closes, so a route naming a permission the host does not declare fails the
// startup rather than deciding nothing at request time.
func declareActivityRoutes(reg *core.Registry) error {
	gate, err := core.Resolve[authz.Gate](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return err
	}
	for _, d := range activityPermissions {
		method, path := splitRoute(d.route)
		gate.Require(method, path, d.perm)
	}
	return nil
}

// What an activity is about. A deal is the one subject so far; the column is a
// kind and an identifier so that another subject is a value rather than a new
// table, and the migration's CHECK and reference hold it to deals until then.
const subjectDeal = "deal"

// The kinds of activity. A stage change is written by the stage move alone, in
// the move's own transaction; the others are what a member records.
const (
	activityNote        = "note"
	activityCall        = "call"
	activityEmail       = "email"
	activityMeeting     = "meeting"
	activityStageChange = "stage-change"
)

// recordedKinds are the kinds a member records through the API.
var recordedKinds = []string{activityNote, activityCall, activityEmail, activityMeeting}

// maxActivityBody bounds the text an activity carries.
const maxActivityBody = 4000

var (
	errBadSubjectKind  = errors.New("the subject kind is not " + subjectDeal)
	errNoSubject       = errors.New("the activity names no subject")
	errBadActivityKind = errors.New("the activity kind is not one of " + strings.Join(recordedKinds, ", "))
	errNoActivityBody  = errors.New("the activity body is empty or longer than 4000 characters")
	errBadActivityID   = errors.New("the activity identifier is not letters, digits, dashes and underscores, at most 64 long")
	errUnknownSubject  = errors.New("the subject is not one of this tenant's deals")
	errActivityExists  = errors.New("an activity with this identifier exists")
	errStageHistory    = errors.New("a stage change is part of the deal's history and is not deleted")
	errNoActivity      = errors.New("no activity has this identifier")
)

//go:embed openapi/activities.json
var activitiesOpenAPI []byte

//go:embed migrations/activities
var activitiesMigrationRoot embed.FS

var activitiesMigrations = subtree(activitiesMigrationRoot, "migrations/activities")

// Activity is one thing that happened to a subject: a note, a call, an email, a
// meeting, or a move of the deal to another stage. It is a scoped model.
//
// EventID and WonEventID are set on a stage change alone. They hold the
// identifiers of the events the move published, so an event a consumer
// received is traced back to the write it announced.
type Activity struct {
	tenant.Model
	ID          string    `gorm:"column:id;primaryKey"`
	SubjectKind string    `gorm:"column:subject_kind;not null"`
	SubjectID   string    `gorm:"column:subject_id;not null"`
	Kind        string    `gorm:"column:kind;not null"`
	Body        string    `gorm:"column:body;not null"`
	ActorID     string    `gorm:"column:actor_id;not null"`
	OccurredAt  time.Time `gorm:"column:occurred_at;not null"`
	EventID     *string   `gorm:"column:event_id"`
	WonEventID  *string   `gorm:"column:won_event_id"`
}

// TableName fixes the table this model maps to.
func (Activity) TableName() string { return activityTable }

// stageChangeBody is the text a stage change records: the two stages, and the
// note the move carried when it carried one.
func stageChangeBody(from string, to PipelineStage, note string) string {
	text := from + " -> " + to.Key
	if note = strings.TrimSpace(note); note != "" {
		text += ": " + note
	}
	return text
}

// activityView is one activity as the API shows it.
type activityView struct {
	ID          string    `json:"id"`
	SubjectKind string    `json:"subject_kind"`
	SubjectID   string    `json:"subject_id"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	ActorID     string    `json:"actor_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	EventID     *string   `json:"event_id"`
	WonEventID  *string   `json:"won_event_id"`
}

func viewOfActivity(a Activity) activityView {
	return activityView{
		ID: a.ID, SubjectKind: a.SubjectKind, SubjectID: a.SubjectID, Kind: a.Kind, Body: a.Body,
		ActorID: a.ActorID, OccurredAt: a.OccurredAt.UTC(), EventID: a.EventID, WonEventID: a.WonEventID,
	}
}

// activityRequest is the body of an activity a member records.
type activityRequest struct {
	// ID is the identifier the client chooses, or empty for one generated
	// here.
	ID          string     `json:"id"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   string     `json:"subject_id"`
	Kind        string     `json:"kind"`
	Body        string     `json:"body"`
	OccurredAt  *time.Time `json:"occurred_at"`
}

// Validate refuses an activity whose values this host does not accept. A stage
// change is among them: it is the stage move's to write.
func (a activityRequest) Validate() error {
	switch {
	case a.ID != "" && !dealIDPattern.MatchString(a.ID):
		return errBadActivityID
	case a.SubjectKind != subjectDeal:
		return errBadSubjectKind
	case strings.TrimSpace(a.SubjectID) == "":
		return errNoSubject
	case !slices.Contains(recordedKinds, a.Kind):
		return errBadActivityKind
	case strings.TrimSpace(a.Body) == "" || len(a.Body) > maxActivityBody:
		return errNoActivityBody
	}
	return nil
}

// activitiesProduct is the activities module's product.
type activitiesProduct struct {
	released
	db db.Database
}

// activityByID reads one of the request's tenant's activities.
func activityByID(ctx context.Context, database db.Database, id string) (Activity, bool, error) {
	var found []Activity
	err := database.DB().WithContext(ctx).Where("id = ?", id).Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return Activity{}, false, err
	}
	return found[0], true, nil
}

// createActivity records an activity on one of the tenant's deals. The row
// names no tenant; the filter fills it, and the subject has to be a deal of the
// same tenant, which the read below answers and the composite reference
// enforces.
func (p *activitiesProduct) createActivity(c *gin.Context) {
	ctx := c.Request.Context()
	logger := log.FromContext(ctx)
	self, ok := memberOf(c, createActivityRoute)
	if !ok {
		return
	}
	var body activityRequest
	if !bindBody(c, &body) {
		return
	}
	_, found, err := dealByID(ctx, p.db, body.SubjectID)
	if err != nil {
		logger.Error("reading an activity's subject", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	if !found {
		writeError(c, nethttp.StatusUnprocessableEntity, errUnknownSubject.Error())
		return
	}
	id := body.ID
	if id == "" {
		if id, err = newID("a_"); err != nil {
			logger.Error("drawing an activity identifier", "error", err)
			writeError(c, nethttp.StatusInternalServerError, "no identifier could be drawn")
			return
		}
	} else {
		// A look-up before the insert answers the ordinary duplicate sooner;
		// the primary key is what refuses a concurrent one, answered as the
		// same conflict below.
		var exists bool
		_, exists, err = activityByID(ctx, p.db, id)
		if err != nil {
			logger.Error("reading an activity", "error", err)
			writeError(c, nethttp.StatusServiceUnavailable, "the activity table cannot be read")
			return
		}
		if exists {
			writeError(c, nethttp.StatusConflict, errActivityExists.Error())
			return
		}
	}
	occurred := time.Now().UTC()
	if body.OccurredAt != nil {
		occurred = body.OccurredAt.UTC()
	}
	activity := Activity{
		ID:          id,
		SubjectKind: body.SubjectKind,
		SubjectID:   body.SubjectID,
		Kind:        body.Kind,
		Body:        body.Body,
		ActorID:     string(self.Account),
		OccurredAt:  occurred,
	}
	err = p.db.DB().WithContext(ctx).Create(&activity).Error
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		// The database module marks the key's refusal on either engine.
		writeError(c, nethttp.StatusConflict, errActivityExists.Error())
		return
	case err != nil:
		logger.Error("writing an activity", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the activity could not be written")
		return
	}
	c.JSON(nethttp.StatusCreated, viewOfActivity(activity))
}

// deleteActivity removes an activity a member recorded. A stage change is
// refused: it is the record of a move, and the deal's history would no longer
// say how it reached its stage.
func (p *activitiesProduct) deleteActivity(c *gin.Context) {
	ctx := c.Request.Context()
	logger := log.FromContext(ctx)
	if _, ok := memberOf(c, deleteActivityRoute); !ok {
		return
	}
	id := c.Param("id")
	result := p.db.DB().WithContext(ctx).Where("id = ? AND kind <> ?", id, activityStageChange).Delete(&Activity{})
	if result.Error != nil {
		logger.Error("deleting an activity", "error", result.Error)
		writeError(c, nethttp.StatusInternalServerError, "the activity could not be deleted")
		return
	}
	if result.RowsAffected == 1 {
		c.Status(nethttp.StatusNoContent)
		return
	}
	_, exists, err := activityByID(ctx, p.db, id)
	switch {
	case err != nil:
		logger.Error("reading an activity", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the activity table cannot be read")
	case exists:
		writeError(c, nethttp.StatusConflict, errStageHistory.Error())
	default:
		writeError(c, nethttp.StatusNotFound, errNoActivity.Error())
	}
}

// timeline is what a deal's timeline answers with.
type timeline struct {
	DealID     string         `json:"deal_id"`
	Activities []activityView `json:"activities"`
}

// dealTimeline answers a deal's activities in the order they occurred. Neither
// statement names a tenant: the deal is one of the request's tenant's, and so
// are the activities the second finds.
func (p *activitiesProduct) dealTimeline(c *gin.Context) {
	ctx := c.Request.Context()
	logger := log.FromContext(ctx)
	if _, ok := memberOf(c, dealTimelineRoute); !ok {
		return
	}
	id := c.Param("id")
	_, found, err := dealByID(ctx, p.db, id)
	if err != nil {
		logger.Error("reading a deal", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the deal table cannot be read")
		return
	}
	if !found {
		writeError(c, nethttp.StatusNotFound, errDealNotFound.Error())
		return
	}
	var activities []Activity
	err = p.db.DB().WithContext(ctx).
		Where("subject_kind = ? AND subject_id = ?", subjectDeal, id).
		Order("occurred_at, id").
		Find(&activities).Error
	if err != nil {
		logger.Error("reading a deal's activities", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the activity table cannot be read")
		return
	}
	out := timeline{DealID: id, Activities: make([]activityView, 0, len(activities))}
	for _, a := range activities {
		out.Activities = append(out.Activities, viewOfActivity(a))
	}
	c.JSON(nethttp.StatusOK, out)
}

// init registers the activities module. It runs on serve: its routes sit on the
// API endpoint.
func init() {
	registerApplication(application{
		module:     activitiesModule(),
		runsOn:     []string{serveCommand},
		models:     []any{Activity{}, Deal{}, PipelineStage{}},
		journalled: []string{createActivityRoute, deleteActivityRoute},
	})
}

// activitiesModule declares the activities table and binds the activity routes.
//
// The activities table references the deals table, so the module requires that
// module's table token, optionally, for the order of the migrations alone: it
// reads the deals table through the database handle. The actor column is an
// account identifier and names no foreign key. It takes up the declaration
// surface optionally: each route declares the permission the authorization
// middleware decides it on from this module's own Init.
func activitiesModule() core.Module {
	return core.Module{
		Name: activitiesModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*dealsTable)(nil), Optional: true},
			{Token: (*authz.Gate)(nil), Optional: true},
		},
		Resources: []any{
			db.Migrations{FS: activitiesMigrations},
			speedhttp.Spec{Endpoint: apiEndpoint, Document: activitiesOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, activitiesModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return &activitiesProduct{released: released{log: logger}, db: database}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*activitiesProduct)
			if !ok {
				return errForeignInstance(activitiesModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			api, err := router.Endpoint(apiEndpoint)
			if err != nil {
				return fmt.Errorf("binding the activity routes: %w", err)
			}
			mount(api, createActivityRoute, product.createActivity)
			mount(api, deleteActivityRoute, product.deleteActivity)
			mount(api, dealTimelineRoute, product.dealTimeline)
			return declareActivityRoutes(reg)
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
