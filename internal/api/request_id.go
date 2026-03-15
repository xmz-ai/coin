package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const requestIDContextKey = "request_id"

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-Id"))
		if requestID == "" {
			requestID = newRequestID()
		}
		c.Set(requestIDContextKey, requestID)
		c.Next()
	}
}

func getRequestID(c *gin.Context) string {
	if c != nil {
		if v, ok := c.Get(requestIDContextKey); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return newRequestID()
}

func newRequestID() string {
	return "req_" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}
