package main

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/store"
)

func adminName(v *verb, o *options) (string, error) {
	if len(o.args) != 1 {
		return "", usagef(v, "takes exactly one admin <name>, got %d arguments", len(o.args))
	}
	if strings.TrimSpace(o.dataDir) == "" {
		return "", usagef(v, "--data-dir is required")
	}
	if err := auth.ValidAdminName(o.args[0]); err != nil {
		return "", usagef(v, "%v", err)
	}
	return o.args[0], nil
}

func openAuth(v *verb, o *options) (*auth.Service, *store.Store, error) {
	if strings.TrimSpace(o.secretKey) == "" {
		return nil, nil, usagef(v, "--secret-key is required; it seals the second factor")
	}
	sealer, err := auth.LoadSealer(o.secretKey)
	if err != nil {
		return nil, nil, fmt.Errorf("--secret-key: %w", err)
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return nil, nil, err
	}
	return auth.New(st, sealer, nil, nil), st, nil
}

func readPassword(e *env, o *options) (string, error) {
	if !o.passwordStdin {
		if e.promptPassword == nil {
			return "", errors.New("stdin is not a terminal; pass --password-stdin and pipe the password in")
		}
		return e.promptPassword(e.stdout)
	}
	if e.stdin == nil {
		return "", errors.New("no stdin to read the password from")
	}
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read the password from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func runAdminAdd(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	name, err := adminName(v, o)
	if err != nil {
		return err
	}
	svc, st, err := openAuth(v, o)
	if err != nil {
		return err
	}
	defer st.Close()
	password, err := readPassword(e, o)
	if err != nil {
		return err
	}
	enrol, err := svc.AddAdmin(name, password)
	if errors.Is(err, store.ErrDuplicate) {
		return fmt.Errorf("admin %q already exists; `admin reset-totp` replaces its second factor, `admin remove` removes it", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "✓ admin %q added\n", name)
	printEnrolment(e, enrol)
	return nil
}

func printEnrolment(e *env, enrol *auth.Enrolment) {
	fmt.Fprintf(e.stdout, "\nAdd this second factor to an authenticator app now. It is shown only here, once;\nthe catalog keeps it sealed and nothing can print it again.\n\n  secret: %s\n  %s\n", enrol.Secret, enrol.OTPAuthURL)
}

func runAdminList(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.dataDir) == "" {
		return usagef(v, "--data-dir is required")
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	admins, err := st.ListAdmins()
	if err != nil {
		return err
	}
	if len(admins) == 0 {
		fmt.Fprintf(e.stdout, "no admins; add one with: %s admin add <name> --data-dir <dir> --secret-key <file>\n", toolName)
		return nil
	}
	fmt.Fprintf(e.stdout, "%-24s %-12s %s\n", "NAME", "ADDED", "SESSIONS")
	for _, a := range admins {
		n, err := st.SessionCount(a.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "%-24s %-12s %d\n", a.Name, a.CreatedAt.Format("2006-01-02"), n)
	}
	return nil
}

func runAdminRemove(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	name, err := adminName(v, o)
	if err != nil {
		return err
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteAdmin(name); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no admin named %q; `%s admin list` shows the ones that exist", name, toolName)
	} else if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "✓ admin %q removed; its sessions ended with it\n", name)
	return nil
}

func runAdminResetTOTP(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	name, err := adminName(v, o)
	if err != nil {
		return err
	}
	svc, st, err := openAuth(v, o)
	if err != nil {
		return err
	}
	defer st.Close()
	enrol, err := svc.ResetTOTP(name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no admin named %q; `%s admin list` shows the ones that exist", name, toolName)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "✓ admin %q has a new second factor; its sessions ended\n", name)
	printEnrolment(e, enrol)
	return nil
}
