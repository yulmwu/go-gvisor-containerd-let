package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrInvalidCursor = errors.New("invalid cursor")

type LogsPage struct {
	Lines      []string `json:"lines"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

func validatePathToken(v string) error {
	if v == "" {
		return fmt.Errorf("empty path token")
	}

	if strings.Contains(v, "/") || strings.Contains(v, "\\") || strings.Contains(v, "..") {
		return fmt.Errorf("invalid path token")
	}

	return nil
}

func (s *Service) containerLogPath(sandboxID, containerName string) (string, error) {
	if err := validatePathToken(sandboxID); err != nil {
		return "", fmt.Errorf("invalid sandbox id")
	}

	if err := validatePathToken(containerName); err != nil {
		return "", fmt.Errorf("invalid container name")
	}

	logDir := filepath.Join(s.cfg.StateBaseDir, sandboxID, "logs")
	path := filepath.Clean(filepath.Join(logDir, containerName+".log"))
	logDirClean := filepath.Clean(logDir)
	if path != logDirClean && !strings.HasPrefix(path, logDirClean+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid log path")
	}

	return path, nil
}

func (s *Service) GetContainerLogs(_ context.Context, sandboxID, containerName, cursor string, limit int) (*LogsPage, error) {
	if sandboxID == "" || containerName == "" {
		return nil, fmt.Errorf("sandbox id and container name are required")
	}

	if limit <= 0 {
		limit = 100
	}

	if limit > 1000 {
		limit = 1000
	}

	offset := int64(0)
	if strings.TrimSpace(cursor) != "" {
		v, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || v < 0 {
			return nil, ErrInvalidCursor
		}
		offset = v
	}

	path, err := s.containerLogPath(sandboxID, containerName)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	r := bufio.NewReader(f)
	lines := make([]string, 0, limit)
	next := offset
	for len(lines) < limit {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			next += int64(len(line))
			lines = append(lines, strings.TrimRight(line, "\r\n"))
		}

		if err == io.EOF {
			return &LogsPage{Lines: lines, NextCursor: strconv.FormatInt(next, 10), HasMore: false}, nil
		}

		if err != nil {
			return nil, err
		}
	}

	_, err = r.Peek(1)
	hasMore := err == nil
	return &LogsPage{Lines: lines, NextCursor: strconv.FormatInt(next, 10), HasMore: hasMore}, nil
}
