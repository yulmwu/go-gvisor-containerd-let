package model

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func ParseCPUMilli(raw string) (int64, error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return 0, fmt.Errorf("cpu is required")
	}

	if strings.HasSuffix(v, "m") {
		n, err := strconv.ParseInt(strings.TrimSuffix(v, "m"), 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid cpu: %s", raw)
		}

		return n, nil
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid cpu: %s", raw)
	}

	milli := int64(math.Round(f * 1000.0))
	if milli <= 0 {
		return 0, fmt.Errorf("invalid cpu: %s", raw)
	}

	return milli, nil
}

func ParseMemoryBytes(raw string) (int64, error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return 0, fmt.Errorf("memory is required")
	}

	mult := int64(1)
	switch {
	case strings.HasSuffix(v, "ki"):
		mult, v = 1024, strings.TrimSuffix(v, "ki")
	case strings.HasSuffix(v, "mi"):
		mult, v = 1024*1024, strings.TrimSuffix(v, "mi")
	case strings.HasSuffix(v, "gi"):
		mult, v = 1024*1024*1024, strings.TrimSuffix(v, "gi")
	case strings.HasSuffix(v, "ti"):
		mult, v = 1024*1024*1024*1024, strings.TrimSuffix(v, "ti")
	case strings.HasSuffix(v, "k"):
		mult, v = 1000, strings.TrimSuffix(v, "k")
	case strings.HasSuffix(v, "m"):
		mult, v = 1000*1000, strings.TrimSuffix(v, "m")
	case strings.HasSuffix(v, "g"):
		mult, v = 1000*1000*1000, strings.TrimSuffix(v, "g")
	case strings.HasSuffix(v, "t"):
		mult, v = 1000*1000*1000*1000, strings.TrimSuffix(v, "t")
	}

	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid memory: %s", raw)
	}

	bytes := int64(math.Round(f * float64(mult)))
	if bytes <= 0 {
		return 0, fmt.Errorf("invalid memory: %s", raw)
	}

	return bytes, nil
}
