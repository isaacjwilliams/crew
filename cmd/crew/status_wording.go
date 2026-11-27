package main

// How crew status words what a line says twice: in full for the plain text,
// and as the live view shows it in its columns.

// wording is something a status line says: text is its full words, as the
// plain text prints them, and live is what the live view shows in its place.
type wording struct{ text, live string }

// same is words that read the same in the plain text and the live view.
func same(s string) wording { return wording{s, s} }
