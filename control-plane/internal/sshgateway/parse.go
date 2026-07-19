// parse.go maps the SSH login name "user+instance" onto a Claworc username
// and a target instance name.

package sshgateway

import "strings"

// ParseSSHUser splits an SSH login name of the form "<username>+<instance>"
// on the FIRST '+'. An empty instance means the client did not specify one.
// Instance names are K8s-safe ([a-z0-9-]) and never contain '+'; Claworc
// usernames containing '+' cannot use the gateway.
func ParseSSHUser(sshUser string) (username, instance string) {
	if i := strings.IndexByte(sshUser, '+'); i >= 0 {
		return sshUser[:i], sshUser[i+1:]
	}
	return sshUser, ""
}

// ResolveInstanceName returns the candidate DB instance names for a
// user-typed instance part, in lookup order. Instance names are stored with
// a "bot-" prefix (see generateName), which users may omit.
func ResolveInstanceName(raw string) []string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return nil
	}
	if strings.HasPrefix(name, "bot-") {
		return []string{name}
	}
	return []string{name, "bot-" + name}
}
