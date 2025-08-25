package health

import (
	"github.com/sachinpaul94/opengist/internal/db"
	"github.com/sachinpaul94/opengist/internal/web/context"
	"time"
)

func Healthcheck(ctx *context.Context) error {
	// Check database connection
	dbOk := "ok"
	httpStatus := 200

	err := db.Ping()
	if err != nil {
		dbOk = "ko"
		httpStatus = 503
	}

	return ctx.JSON(httpStatus, map[string]interface{}{
		"opengist": "ok",
		"database": dbOk,
		"time":     time.Now().Format(time.RFC3339),
	})
}
