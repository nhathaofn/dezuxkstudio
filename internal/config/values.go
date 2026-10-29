package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// value is a typed binding to one Config field. It implements flag.Value so the
// same parser serves flags, environment variables and the config file.
type value interface {
	String() string
	Set(string) error
}

// listValue is implemented by values that accept JSON arrays from the config file.
type listValue interface {
	SetList([]string) error
}

type stringValue struct{ p *string }

func (v stringValue) String() string     { return *v.p }
func (v stringValue) Set(s string) error { *v.p = strings.TrimSpace(s); return nil }

type intValue struct{ p *int }

func (v intValue) String() string { return strconv.Itoa(*v.p) }
func (v intValue) Set(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("expected integer, got %q", s)
	}
	*v.p = n
	return nil
}

type int64Value struct{ p *int64 }

func (v int64Value) String() string { return strconv.FormatInt(*v.p, 10) }
func (v int64Value) Set(s string) error {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("expected integer, got %q", s)
	}
	*v.p = n
	return nil
}

type boolValue struct{ p *bool }

func (v boolValue) String() string   { return strconv.FormatBool(*v.p) }
func (v boolValue) IsBoolFlag() bool { return true }
func (v boolValue) Set(s string) error {
	b, err := strconv.ParseBool(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("expected boolean, got %q", s)
	}
	*v.p = b
	return nil
}

type durationValue struct{ p *time.Duration }

func (v durationValue) String() string { return v.p.String() }
func (v durationValue) Set(s string) error {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("expected duration like \"30s\", got %q", s)
	}
	*v.p = d
	return nil
}

type listOfStrings struct{ p *[]string }

func (v listOfStrings) String() string { return strings.Join(*v.p, ",") }
func (v listOfStrings) Set(s string) error {
	return v.SetList(strings.Split(s, ","))
}
func (v listOfStrings) SetList(items []string) error {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if t := strings.TrimSpace(it); t != "" {
			out = append(out, t)
		}
	}
	*v.p = out
	return nil
}

type modeValue struct{ p *Mode }

func (v modeValue) String() string { return string(*v.p) }
func (v modeValue) Set(s string) error {
	m, err := ParseMode(s)
	if err != nil {
		return err
	}
	*v.p = m
	return nil
}
