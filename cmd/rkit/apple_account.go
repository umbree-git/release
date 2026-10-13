package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var appleAccountFiles = []string{"config/apple-account", "config/apple.account"}

var appleHomeFiles = []string{"config/apple-home", "config/apple.home"}

func loadAppleAccount(repoDir string) error {
	if dir := strings.TrimSpace(os.Getenv("APPLE_ACCOUNT_DIR")); dir != "" {
		if err := checkAccountDir(dir, "APPLE_ACCOUNT_DIR"); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "→ Apple account dir (from APPLE_ACCOUNT_DIR): %s\n", dir)
		return nil
	}
	account, err := resolveAppleValue(repoDir, "APPLE_ACCOUNT", appleAccountFiles,
		"the Apple account plugin folder name")
	if err != nil {
		return err
	}
	home, err := resolveAppleValue(repoDir, "APPLE_HOME", appleHomeFiles,
		"the absolute directory holding one folder per Apple account")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(home) {
		return appleErrorf("APPLE_HOME resolved to %q, which is not an absolute path", home)
	}
	accountDir := filepath.Join(home, account)
	if err := checkAccountDir(accountDir, "the account plugin folder"); err != nil {
		return err
	}
	_ = os.Setenv("APPLE_ACCOUNT", account)
	_ = os.Setenv("APPLE_HOME", home)
	_ = os.Setenv("APPLE_ACCOUNT_DIR", accountDir)
	fmt.Fprintf(os.Stderr, "→ Apple account: %s (%s)\n", account, accountDir)
	return nil
}

func resolveAppleValue(repoDir, env string, files []string, holds string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v, nil
	}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(repoDir, name))
		if err != nil {
			continue
		}
		if v := firstConfigLine(string(body)); v != "" {
			return v, nil
		}
		return "", appleErrorf("%s holds no value — only blank or comment lines.\n%s",
			filepath.Join(repoDir, name), appleFixHint(repoDir, env, files, holds))
	}
	return "", appleErrorf("%s is unresolved.\n%s", env, appleFixHint(repoDir, env, files, holds))
}

func firstConfigLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func appleFixHint(repoDir, env string, files []string, holds string) string {
	return fmt.Sprintf("  supply it as one of:\n"+
		"    %s\n        one line: %s\n"+
		"    $%s\n        the same value, from the environment\n"+
		"    $APPLE_ACCOUNT_DIR\n        the account's folder itself, which settles account and home together",
		filepath.Join(repoDir, files[0]), holds, env)
}

func checkAccountDir(dir, label string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return appleErrorf("%s points at %s, which cannot be read: %v", label, dir, err)
	}
	if !info.IsDir() {
		return appleErrorf("%s points at %s, which is not a directory", label, dir)
	}
	return nil
}

func appleErrorf(format string, args ...any) error {
	return fmt.Errorf("apple signing requested (--apple/--public) but "+format+
		"\n  refusing to continue: signing with no account plugin produces an AD-HOC build "+
		"that looks Developer-ID signed", args...)
}
