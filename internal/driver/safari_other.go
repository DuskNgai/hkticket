//go:build !darwin

package driver

import "fmt"

// NewSafari 在非 macOS 平台上不可用
func NewSafari() Driver {
	return unavailableDriver{"Safari 驱动仅在 macOS 上可用"}
}

type unavailableDriver struct{ reason string }

func (u unavailableDriver) OpenTabs(string) error       { return fmt.Errorf("%s", u.reason) }
func (u unavailableDriver) GetContent() (string, error) { return "", fmt.Errorf("%s", u.reason) }
func (u unavailableDriver) ClickEntry() (string, error) { return "", fmt.Errorf("%s", u.reason) }
func (u unavailableDriver) Reload() error               { return fmt.Errorf("%s", u.reason) }
func (u unavailableDriver) BringToFront() error         { return fmt.Errorf("%s", u.reason) }
func (u unavailableDriver) Close() error                { return nil }
