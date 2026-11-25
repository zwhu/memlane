//go:build !darwin

package main

func wasOpenedAtLoginAtReady() bool { return false }
