//go:build !e2e || !darwin

package main

import "github.com/egoist/mygo"

func configureNativeProbe(*mygo.Window) {}
