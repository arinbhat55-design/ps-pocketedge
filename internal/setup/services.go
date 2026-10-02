package setup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func xmlText(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func launchPlist(label string, args []string, env map[string]string, root string, auto, keep bool) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>`)
	b.WriteString("<key>Label</key><string>" + xmlText(label) + "</string><key>ProgramArguments</key><array>")
	for _, arg := range args {
		b.WriteString("<string>" + xmlText(arg) + "</string>")
	}
	b.WriteString("</array><key>EnvironmentVariables</key><dict>")
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("<key>" + xmlText(k) + "</key><string>" + xmlText(env[k]) + "</string>")
	}
	b.WriteString("<key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>")
	for k, v := range map[string]bool{"RunAtLoad": auto, "KeepAlive": auto && keep} {
		b.WriteString("<key>" + k + "</key>")
		if v {
			b.WriteString("<true/>")
		} else {
			b.WriteString("<false/>")
		}
	}
	b.WriteString("<key>WorkingDirectory</key><string>" + xmlText(root) + "</string>")
	name := strings.TrimPrefix(label, "com.pspocketedge.setup.")
	for _, k := range []string{"StandardOutPath", "StandardErrorPath"} {
		b.WriteString("<key>" + k + "</key><string>" + xmlText(filepath.Join(root, name+".log")) + "</string>")
	}
	b.WriteString("</dict></plist>")
	return []byte(b.String())
}

func (e *Engine) job(ctx context.Context, p Plan, name string, args []string, env map[string]string, keep bool) error {
	home, _ := os.UserHomeDir()
	label := "com.pspocketedge.setup." + name
	dir := filepath.Join(home, "Library/LaunchAgents")
	if err := safeDirectory(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, label+".plist")
	if existing, err := os.ReadFile(path); err == nil && !strings.Contains(string(existing), "<string>"+xmlText(p.DataPath)+"</string>") {
		return errors.New("an existing setup uses a different data directory; use its original directory")
	}
	plist := launchPlist(label, args, env, p.DataPath, p.AutoStart, keep)
	if err := atomicWrite(filepath.Join(p.DataPath, name+".plist"), plist, 0600); err != nil {
		return err
	}
	if err := atomicWrite(path, plist, 0600); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	service := domain + "/" + label
	// bootout only our own named job; never touch user-installed services.
	e.command(ctx, "/bin/launchctl", "bootout", service)
	if _, err := e.command(ctx, "/bin/launchctl", "bootstrap", domain, path); err != nil {
		return err
	}
	if !p.AutoStart {
		_, err := e.command(ctx, "/bin/launchctl", "kickstart", service)
		return err
	}
	return nil
}

func (e *Engine) installApp(ctx context.Context, p Plan) (string, string, error) {
	source := filepath.Join(e.Payload, "PS-pocketEdge.app")
	if _, err := os.Stat(p.AppPath); err == nil {
		id, err := e.command(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(p.AppPath, "Contents/Info.plist"))
		if err != nil || id != "com.pspocketedge.app" {
			return "", "", errors.New("destination contains another app; choose a different folder")
		}
		if _, verifyErr := e.command(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", p.AppPath); verifyErr == nil {
			sourceCode, sourceErr := e.command(ctx, "/usr/bin/codesign", "-dvv", source)
			destinationCode, destErr := e.command(ctx, "/usr/bin/codesign", "-dvv", p.AppPath)
			hash := regexp.MustCompile(`(?m)^CDHash=([a-f0-9]+)$`)
			sourceHash := hash.FindStringSubmatch(sourceCode)
			destHash := hash.FindStringSubmatch(destinationCode)
			if sourceErr == nil && destErr == nil && len(sourceHash) == 2 && len(destHash) == 2 && sourceHash[1] == destHash[1] {
				version, err := e.command(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleShortVersionString", filepath.Join(p.AppPath, "Contents/Info.plist"))
				return "Already available", version, err
			}
		}
	}
	if _, err := e.command(ctx, "/usr/bin/pgrep", "-x", "PS-pocketEdge"); err == nil {
		return "", "", errors.New("quit the running PS-pocketEdge app, then retry setup")
	}
	parent := filepath.Dir(p.AppPath)
	stage := filepath.Join(parent, fmt.Sprintf(".PS-pocketEdge-%d.app", os.Getpid()))
	backup := filepath.Join(parent, fmt.Sprintf("PS-pocketEdge-previous-%d.app", time.Now().UnixNano()))
	// Quote every path; admin authorization is confined to the app copy/swap.
	command := "/bin/mkdir -p " + shellQuote(parent) + " && /usr/bin/ditto " + shellQuote(source) + " " + shellQuote(stage) + " && /usr/bin/codesign --verify --deep --strict " + shellQuote(stage) + " && if [ -e " + shellQuote(p.AppPath) + " ]; then /bin/mv " + shellQuote(p.AppPath) + " " + shellQuote(backup) + "; fi && if /bin/mv " + shellQuote(stage) + " " + shellQuote(p.AppPath) + "; then exit 0; else if [ -e " + shellQuote(backup) + " ]; then /bin/mv " + shellQuote(backup) + " " + shellQuote(p.AppPath) + "; fi; exit 1; fi"
	var err error
	if strings.HasPrefix(parent, "/Applications") {
		_, err = e.command(ctx, "/usr/bin/osascript", "-e", "do shell script "+appleQuote(command)+" with administrator privileges")
	} else {
		_, err = e.command(ctx, "/bin/sh", "-c", command)
	}
	if err != nil {
		return "", "", errors.New("app installation failed or administrator permission was declined; previous app and data are preserved")
	}
	version, err := e.command(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleShortVersionString", filepath.Join(p.AppPath, "Contents/Info.plist"))
	return "Installed", version, err
}
