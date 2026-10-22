package main

// machineName is this machine's name in the lease when CREW_HOST isn't set: its
// short hostname.
func machineName() string { return shortHostname() }
