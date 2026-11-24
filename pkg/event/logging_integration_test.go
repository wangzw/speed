//go:build integration

package event_test

import (
	"testing"

	eventnats "github.com/vislake/speed/pkg/event/nats"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
)

func init() {
	loggingBackends = append(loggingBackends,
		loggingBackend{eventredis.ModuleName, func(t *testing.T) pointedAt {
			addr, db := redistest.Acquire(t)
			return pointedAt{redisAddr: addr, redisDB: db}
		}},
		loggingBackend{eventpostgres.ModuleName, func(t *testing.T) pointedAt {
			return pointedAt{dsn: pgtest.Acquire(t)}
		}},
		loggingBackend{eventnats.ModuleName, func(t *testing.T) pointedAt {
			return pointedAt{natsURL: natstest.URL(t)}
		}},
	)
}
