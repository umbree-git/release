//go:build linux

package main

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var unitKeys = map[string]bool{
	"After": true, "CapabilityBoundingSet": true, "Description": true, "EnvironmentFile": true,
	"ExecStart": true, "Group": true, "LockPersonality": true, "MemoryDenyWriteExecute": true,
	"NoNewPrivileges": true, "OnCalendar": true, "Persistent": true, "PrivateDevices": true,
	"PrivateTmp": true, "ProtectControlGroups": true, "ProtectHome": true,
	"ProtectKernelModules": true, "ProtectKernelTunables": true, "ProtectSystem": true,
	"RandomizedDelaySec": true, "ReadWritePaths": true, "Restart": true, "RestartSec": true,
	"RestrictAddressFamilies": true, "RestrictNamespaces": true, "SystemCallArchitectures": true,
	"Type": true, "UMask": true, "User": true, "WantedBy": true, "Wants": true,
}

var placeholder = regexp.MustCompile(`<[A-Za-z_]+>`)

func checkUnit(name, text, serviceUser string) error {
	hasUser := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if err := checkDirective(name, key, value, serviceUser); err != nil {
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

func checkDirective(name, key, value, serviceUser string) error {
	if key == "PermissionsStartOnly" {
		return fmt.Errorf("%s sets PermissionsStartOnly", name)
	}
	if !unitKeys[key] {
		return fmt.Errorf("%s sets %s=, which the templates do not use", name, key)
	}
	if (key == "User" || key == "Group") && (value != serviceUser || value == "" || value == "root" || value == "0") {
		return fmt.Errorf("%s renders %s=%s, want %s", name, key, value, serviceUser)
	}
	if strings.HasPrefix(key, "Exec") && strings.ContainsAny(strings.SplitN(value, "/", 2)[0], "+!") {
		return fmt.Errorf("%s gives %s a privileged prefix: %s", name, key, value)
	}
	return nil
}
