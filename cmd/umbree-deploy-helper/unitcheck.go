//go:build linux

package main

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

func keySet(keys ...string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

var serviceKeys = []string{
	"CapabilityBoundingSet", "Description", "EnvironmentFile", "ExecStart", "Group", "LockPersonality",
	"MemoryDenyWriteExecute", "NoNewPrivileges", "PrivateDevices", "PrivateTmp", "ProtectControlGroups",
	"ProtectHome", "ProtectKernelModules", "ProtectKernelTunables", "ProtectSystem", "ReadWritePaths",
	"RestrictAddressFamilies", "RestrictNamespaces", "SystemCallArchitectures", "Type", "UMask", "User",
	"After", "Wants",
}

var unitKeys = map[string]map[string]bool{
	serviceUnit:                            keySet(append(serviceKeys, "Restart", "RestartSec", "WantedBy")...),
	"umbree-release-manage-retain.service": keySet(serviceKeys...),
	"umbree-release-manage-retain.timer":   keySet("Description", "OnCalendar", "RandomizedDelaySec", "Persistent", "WantedBy"),
}

func templateKeys(text string) map[string]bool {
	out := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
			out[strings.TrimSpace(k)] = true
		}
	}
	return out
}

func allowedKeys(name, template string) map[string]bool {
	out := map[string]bool{}
	for k := range templateKeys(template) {
		if unitKeys[name][k] {
			out[k] = true
		}
	}
	return out
}

var placeholder = regexp.MustCompile(`<[A-Za-z_]+>`)

func checkUnit(name, text string, allowed map[string]bool, serviceUser string) error {
	hasUser := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if err := checkDirective(name, key, value, allowed, serviceUser); err != nil {
			return err
		}
		hasUser = hasUser || key == "User"
	}
	if strings.HasSuffix(name, ".service") && !hasUser {
		return fmt.Errorf("%s renders no User=", name)
	}
	if m := placeholder.FindString(text); m != "" {
		return fmt.Errorf("%s keeps an unfilled placeholder %s", name, m)
	}
	return nil
}

func checkDirective(name, key, value string, allowed map[string]bool, serviceUser string) error {
	if key == "PermissionsStartOnly" {
		return fmt.Errorf("%s sets PermissionsStartOnly", name)
	}
	if !allowed[key] {
		return fmt.Errorf("%s sets %s=, which its template set does not allow", name, key)
	}
	if (key == "User" || key == "Group") && (value != serviceUser || value == "" || value == "root" || value == "0") {
		return fmt.Errorf("%s renders %s=%s, want %s", name, key, value, serviceUser)
	}
	if strings.HasPrefix(key, "Exec") && strings.ContainsAny(strings.SplitN(value, "/", 2)[0], "+!") {
		return fmt.Errorf("%s gives %s a privileged prefix: %s", name, key, value)
	}
	return nil
}
