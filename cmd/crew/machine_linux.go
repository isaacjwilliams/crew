package main

// machineName is this machine's name in the lease when CREW_HOST isn't set: its
// short hostname.
func machineName() string { return shortHostname() }

// installTools is how crew install suggests installing the tools it's missing.
const installTools = "on CachyOS: sudo pacman -S --needed git github-cli fish curl"
