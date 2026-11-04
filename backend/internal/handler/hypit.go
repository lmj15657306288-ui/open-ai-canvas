package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// hypitWorkerToken 是执行节点与画布之间的共享密钥。
// 未配置时 worker 接口一律拒绝，避免任何人都能把任务标成已完成。
func hypitWorkerToken() string {
	return strings.TrimSpace(os.Getenv("HYPIT_WORKER_TOKEN"))
}

func hypitWorkerAuthorized(c *gin.Context) bool {
	expected := hypitWorkerToken()
	if expected == "" {
		return false
	}
	return subtleCompare(c.GetHeader("X-Hypit-Worker-Token"), expected)
}

// subtleCompare 固定时间比较，避免按字节泄露密钥。
func subtleCompare(a string, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func RegisterHypitRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/hypit/tasks", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.HypitTaskCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		task, err := svc.CreateHypitTask(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})

	r.GET("/hypit/tasks", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, limit, err := parsePaginationQuery(c, 20)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.UserHypitTaskPage(user, page, limit)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})

	r.GET("/hypit/tasks/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		task, err := svc.UserHypitTask(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})

	r.POST("/hypit/tasks/:id/cancel", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		task, err := svc.CancelUserHypitTask(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})

	// ---- 执行节点接口（独立密钥鉴权，不绑用户会话）----
	worker := r.Group("/hypit/worker", func(c *gin.Context) {
		if !hypitWorkerAuthorized(c) {
			fail(c, http.StatusUnauthorized, errors.New("执行节点鉴权失败"))
			c.Abort()
		}
	})

	worker.POST("/heartbeat", func(c *gin.Context) {
		var req struct {
			WorkerID string `json:"workerId"`
			Host     string `json:"host"`
			Version  string `json:"version"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := svc.HypitWorkerHeartbeat(req.WorkerID, req.Host, req.Version); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})

	worker.POST("/claim", func(c *gin.Context) {
		var req struct {
			WorkerID string `json:"workerId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		task, err := svc.ClaimHypitTask(req.WorkerID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})

	worker.POST("/tasks/:id/report", func(c *gin.Context) {
		var req struct {
			WorkerID         string `json:"workerId"`
			Status           string `json:"status"`
			Progress         int    `json:"progress"`
			Stage            string `json:"stage"`
			OutputURL        string `json:"outputUrl"`
			OutputResourceID string `json:"outputResourceId"`
			Error            string `json:"error"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		task, err := svc.ReportHypitTask(req.WorkerID, c.Param("id"), service.HypitWorkerReportRequest{
			Status:           req.Status,
			Progress:         req.Progress,
			Stage:            req.Stage,
			OutputURL:        req.OutputURL,
			OutputResourceID: req.OutputResourceID,
			Error:            req.Error,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})
}
