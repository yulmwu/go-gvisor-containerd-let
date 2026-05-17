package httpserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-let/sandbox"

	"github.com/gin-gonic/gin"
)

// healthz godoc
// @Summary Health check
// @Description Returns service health.
// @Tags sandboxd-node
// @Produce json
// @Success 200 {object} HealthResponse
// @Router /healthz [get]
func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{OK: true})
}

// nodeStatus godoc
// @Summary Get node resource snapshot
// @Description Returns unified node resource status used by orchestrator heartbeats and scheduling.
// @Tags sandboxd-node
// @Produce json
// @Success 200 {object} NodeStatusResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/node/status [get]
func (s *Server) nodeStatus(c *gin.Context) {
	snap, err := s.svc.NodeResourceSnapshot(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, NodeStatusResponse{OK: true, Resources: snap, ExternalIP: s.ipSvc.Lookup(c.Request.Context())})
}

// createSandbox godoc
// @Summary Create sandbox (async)
// @Description Validates request, persists state, and starts asynchronous provisioning using CRI + runsc(gVisor).
// @Tags sandboxd-sandbox
// @Accept json
// @Produce json
// @Param request body model.CreateSandboxRequest true "Create sandbox request"
// @Success 202 {object} CreateSandboxResponse
// @Failure 400 {object} ErrorResponse
// @Router /v1/sandboxes [post]
func (s *Server) createSandbox(c *gin.Context) {
	var req model.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	if req.ID == "" {
		req.ID = "sbx-" + time.Now().UTC().Format("20060102-150405")
	}

	opCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	sbx, err := s.svc.CreateSandboxAsync(opCtx, req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusAccepted, CreateSandboxResponse{
		Sandbox:    sbx,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

// getSandbox godoc
// @Summary Get sandbox
// @Description Returns current sandbox state, including phase and container runtime status.
// @Tags sandboxd-sandbox
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} GetSandboxResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/sandboxes/{id} [get]
func (s *Server) getSandbox(c *gin.Context) {
	sbx, err := s.svc.GetSandbox(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondErrorMessage(c, http.StatusNotFound, "not found")
			return
		}

		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, GetSandboxResponse{
		Sandbox:    sbx,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

// listSandboxes godoc
// @Summary List sandboxes
// @Description Lists sandboxes with cursor-based pagination.
// @Tags sandboxd-sandbox
// @Produce json
// @Param cursor query string false "Pagination cursor"
// @Param limit query int false "Page size (default 20)"
// @Success 200 {object} ListSandboxesResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/sandboxes [get]
func (s *Server) listSandboxes(c *gin.Context) {
	cursor := c.Query("cursor")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, nextCursor, err := s.svc.ListSandboxesPage(c.Request.Context(), cursor, limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, ListSandboxesResponse{
		Items:      items,
		NextCursor: nextCursor,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

// sandboxStatuses godoc
// @Summary Batch get sandbox status summary
// @Description Returns lightweight sandbox/container status summaries for requested IDs.
// @Tags sandboxd-sandbox
// @Accept json
// @Produce json
// @Param request body SandboxStatusesRequest true "Sandbox status request"
// @Success 200 {object} SandboxStatusesResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/sandboxes/statuses [post]
func (s *Server) sandboxStatuses(c *gin.Context) {
	var req SandboxStatusesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusOK, SandboxStatusesResponse{Items: []SandboxSyncStatus{}, Missing: []string{}, ExternalIP: s.ipSvc.Lookup(c.Request.Context())})
		return
	}

	seen := make(map[string]struct{}, len(req.IDs))
	ids := make([]string, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}

		if _, exists := seen[id]; exists {
			continue
		}

		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	if len(ids) > 200 {
		respondErrorMessage(c, http.StatusBadRequest, "too many ids (max 200)")
		return
	}

	items := make([]SandboxSyncStatus, 0, len(ids))
	missing := make([]string, 0)
	for _, id := range ids {
		sbx, err := s.svc.GetSandbox(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, id)
				continue
			}
			respondError(c, http.StatusInternalServerError, err)
			return
		}

		items = append(items, toSandboxSyncStatus(sbx))
	}

	slices.Sort(missing)
	c.JSON(http.StatusOK, SandboxStatusesResponse{
		Items:      items,
		Missing:    missing,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

func toSandboxSyncStatus(sbx *model.Sandbox) SandboxSyncStatus {
	out := SandboxSyncStatus{
		ID:    sbx.ID,
		Phase: sbx.Phase,
		Error: sbx.Error,
	}

	if len(sbx.Containers) == 0 {
		return out
	}

	for _, st := range sbx.Containers {
		phase := strings.ToLower(strings.TrimSpace(st.Phase))
		if (phase == "running" || phase == "creating") && strings.TrimSpace(st.Error) == "" {
			continue
		}
		out.UnhealthyContainers = append(out.UnhealthyContainers, ContainerSyncStatus{
			Name:       st.Name,
			Phase:      st.Phase,
			Error:      st.Error,
			TaskStatus: st.TaskStatus,
		})
	}
	return out
}

// deleteSandbox godoc
// @Summary Delete sandbox
// @Description Deletes sandbox runtime artifacts and persisted state.
// @Tags sandboxd-sandbox
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} DeleteSandboxResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/sandboxes/{id} [delete]
func (s *Server) deleteSandbox(c *gin.Context) {
	opCtx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	if err := s.svc.DeleteSandbox(opCtx, c.Param("id")); err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, DeleteSandboxResponse{
		ID:         c.Param("id"),
		Phase:      "deleted",
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

// reconcile godoc
// @Summary Trigger reconcile
// @Description Triggers one reconcile pass for orphan/runtime consistency cleanup.
// @Tags sandboxd-maintenance
// @Produce json
// @Success 200 {object} ReconcileResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/reconcile [post]
func (s *Server) reconcile(c *gin.Context) {
	opCtx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	if err := s.svc.ReconcileOnce(opCtx); err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, ReconcileResponse{
		OK:         true,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}

// getContainerLogs godoc
// @Summary Get container logs
// @Description Reads container logs with cursor pagination over byte offsets.
// @Tags sandboxd-logs
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param name path string true "Container name"
// @Param cursor query string false "Cursor offset"
// @Param limit query int false "Max lines (default 100)"
// @Success 200 {object} ContainerLogsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /v1/sandboxes/{id}/containers/{name}/logs [get]
func (s *Server) getContainerLogs(c *gin.Context) {
	cursor := c.Query("cursor")
	limitRaw := c.Query("limit")
	limit := 100
	if limitRaw != "" {
		n, err := strconv.Atoi(limitRaw)
		if err != nil || n < 0 {
			respondErrorMessage(c, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}

	sandboxID := c.Param("id")
	containerName := c.Param("name")

	page, err := s.svc.GetContainerLogs(c.Request.Context(), sandboxID, containerName, cursor, limit)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			sbx, sbErr := s.svc.GetSandbox(c.Request.Context(), sandboxID)
			if sbErr == nil {
				if _, ok := sbx.Containers[containerName]; ok {
					c.JSON(http.StatusOK, ContainerLogsResponse{
						SandboxID: sandboxID,
						Container: containerName,
						Logs: &sandbox.LogsPage{
							Lines:      []string{},
							NextCursor: "0",
							HasMore:    false,
						},
						ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
					})
					return
				}
			}

			respondErrorMessage(c, http.StatusNotFound, "logs not found")
			return
		}

		if errors.Is(err, sandbox.ErrInvalidCursor) {
			respondErrorMessage(c, http.StatusBadRequest, "invalid cursor")
			return
		}

		respondErrorMessage(c, http.StatusInternalServerError, "failed to read logs")
		return
	}

	c.JSON(http.StatusOK, ContainerLogsResponse{
		SandboxID:  sandboxID,
		Container:  containerName,
		Logs:       page,
		ExternalIP: s.ipSvc.Lookup(c.Request.Context()),
	})
}
