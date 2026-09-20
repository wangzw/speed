package core

import "context"

// Stage names one step of the lifecycle. A drive advances from StagePrepare
// through the stage its Depth names and no further, so the values are ordered
// and comparable: a callback runs when its stage is at most the depth.
//
// Stop and Close are not values here. They are the two beats of shutting
// down, run at the end of every drive whatever depth it asked for; making
// them depths to stop at would let a drive declare that it does not release
// what it built.
type Stage uint8

const (
	// StagePrepare is the stance stage: every participating module says
	// whether it runs. It is the zero value, so a Drive that leaves Depth
	// out advances no further than this.
	StagePrepare Stage = iota
	// StageNew constructs the products of the enabled set, in dependency
	// order.
	StageNew
	// StageMigrate applies the migrations collected from the enabled set.
	StageMigrate
	// StageInit wires modules together and publishes.
	StageInit
	// StageStart starts each module's own service, without external
	// traffic.
	StageStart
	// StageServe begins accepting external traffic. It is the deepest a
	// drive can go.
	StageServe
)

// String renders the stage the way the callback it drives is named, so a
// stage failure reads as the field the module wrote.
func (s Stage) String() string {
	switch s {
	case StagePrepare:
		return "Prepare"
	case StageNew:
		return "New"
	case StageMigrate:
		return "Migrate"
	case StageInit:
		return "Init"
	case StageStart:
		return "Start"
	case StageServe:
		return "Serve"
	default:
		return "unknown"
	}
}

// callbackFor returns the callback a module declared for a stage, or nil when
// it declared none.
//
// It covers the stages that share one callback shape. Prepare returns a
// stance and New returns a product, so each has its own signature and its own
// place in the drive; asking for either here returns nil.
func callbackFor(m Module, s Stage) func(context.Context, *Registry, any) error {
	switch s {
	case StageMigrate:
		return m.Migrate
	case StageInit:
		return m.Init
	case StageStart:
		return m.Start
	case StageServe:
		return m.Serve
	default:
		return nil
	}
}
