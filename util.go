package main

func ChompString(s string) (r string) {
	var l int
	for l = len(s); l > 0; l-- {
		if s[l-1] != '\r' &&s[l-1] != '\n' {
			break
		}
	}
	r = s[:l]
	return
}
