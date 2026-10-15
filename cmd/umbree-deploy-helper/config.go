//go:build linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type config struct {
	serviceUser string
	deployUser  string
	stageDir    string
	dataDir     string
	envFile     string
	staticDir   string
	listen      string
	port        string
}

var (
	accountName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	loopback    = regexp.MustCompile(`^127\.0\.0\.1:([0-9]{1,5})$`)
)

func parseConfig(data []byte) (*config, error) {
	values := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("helper.conf: not KEY=VALUE: %q", line)
		}
		if _, dup := values[k]; dup {
			return nil, fmt.Errorf("helper.conf: %s set twice", k)
		}
		values[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return configFrom(values)
}

func configFrom(values map[string]string) (*config, error) {
	cfg := &config{}
	fields := map[string]*string{
		"SERVICE_USER": &cfg.serviceUser, "DEPLOY_USER": &cfg.deployUser,
		"STAGE_DIR": &cfg.stageDir, "MANAGE_DATA_DIR": &cfg.dataDir,
		"MANAGE_ENV_FILE": &cfg.envFile, "STATIC_DIR": &cfg.staticDir,
		"MANAGE_LISTEN": &cfg.listen,
	}
	for k := range values {
		if _, known := fields[k]; !known {
			return nil, fmt.Errorf("helper.conf: unknown key %s", k)
		}
	}
	for k, p := range fields {
		v, set := values[k]
		if !set || v == "" {
			return nil, fmt.Errorf("helper.conf: %s is missing", k)
		}
		*p = v
	}
	return cfg, cfg.validate()
}

func (c *config) validate() error {
	for k, v := range map[string]string{"SERVICE_USER": c.serviceUser, "DEPLOY_USER": c.deployUser} {
		if !accountName.MatchString(v) || v == "root" {
			return fmt.Errorf("helper.conf: %s %q is not an ordinary account name", k, v)
		}
	}
	if c.serviceUser == c.deployUser {
		return fmt.Errorf("helper.conf: SERVICE_USER and DEPLOY_USER must differ")
	}
	paths := map[string]string{"STAGE_DIR": c.stageDir, "MANAGE_DATA_DIR": c.dataDir, "MANAGE_ENV_FILE": c.envFile, "STATIC_DIR": c.staticDir}
	for k, v := range paths {
		if !filepath.IsAbs(v) || filepath.Clean(v) != v || v == "/" {
			return fmt.Errorf("helper.conf: %s %q is not a clean absolute path", k, v)
		}
	}
	m := loopback.FindStringSubmatch(c.listen)
	if m == nil {
		return fmt.Errorf("helper.conf: MANAGE_LISTEN %q is not 127.0.0.1:<port>", c.listen)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("helper.conf: MANAGE_LISTEN port %q is out of range", m[1])
	}
	c.port = m[1]
	return nil
}
