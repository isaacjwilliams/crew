package main

import "syscall"

// machineName is this machine's name in the lease when CREW_HOST isn't set: its
// short hostname.
func machineName() string { return shortHostname() }

// installTools is how crew install suggests installing the tools it's missing.
const installTools = "on CachyOS: sudo pacman -S --needed git github-cli curl"

// ioctlGetTermios is the ioctl that reads a terminal's settings, for stdinIsTerminal.
const ioctlGetTermios = syscall.TCGETS
