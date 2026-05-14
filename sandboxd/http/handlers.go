package httpserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"sandboxd-o/sandboxd/model"
	"sandboxd-o/sandboxd/sandbox"

	"github.com/gin-gonic/gin"
)

func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) nodeStatus(c *gin.Context) {
	snap, err := s.svc.NodeResourceSnapshot(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "resources": snap})
}

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

	respondJSON(c, http.StatusAccepted, gin.H{"sandbox": sbx}, s.ipSvc.Lookup(c.Request.Context()))
}

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

	respondJSON(c, http.StatusOK, gin.H{"sandbox": sbx}, s.ipSvc.Lookup(c.Request.Context()))
}

func (s *Server) listSandboxes(c *gin.Context) {
	cursor := c.Query("cursor")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, nextCursor, err := s.svc.ListSandboxesPage(c.Request.Context(), cursor, limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor}, s.ipSvc.Lookup(c.Request.Context()))
}

func (s *Server) deleteSandbox(c *gin.Context) {
	opCtx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	if err := s.svc.DeleteSandbox(opCtx, c.Param("id")); err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"id": c.Param("id"), "phase": "deleted"}, s.ipSvc.Lookup(c.Request.Context()))
}

func (s *Server) reconcile(c *gin.Context) {
	opCtx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	if err := s.svc.ReconcileOnce(opCtx); err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"ok": true}, s.ipSvc.Lookup(c.Request.Context()))
}

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
					respondJSON(c, http.StatusOK, gin.H{
						"sandbox_id": sandboxID,
						"container":  containerName,
						"logs": gin.H{
							"lines":       []string{},
							"next_cursor": "0",
							"has_more":    false,
						},
					}, s.ipSvc.Lookup(c.Request.Context()))
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

	respondJSON(c, http.StatusOK, gin.H{"sandbox_id": sandboxID, "container": containerName, "logs": page}, s.ipSvc.Lookup(c.Request.Context()))
}
