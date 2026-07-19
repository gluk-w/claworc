// auth.go authenticates inbound SSH connections against per-user public
// keys and authorizes access to the requested instance.
//
// Authorization failures for a *valid* key are "soft": the connection is
// accepted and the failure reason is stashed in Permissions.Extensions so
// the session channel can print an actionable message (a hard reject would
// only show the client an opaque "Permission denied (publickey)").

package sshgateway

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

// Permissions.Extensions keys set by authenticate.
const (
	extUserID     = "claworc-user-id"
	extUsername   = "claworc-username"
	extKeyID      = "claworc-key-id"
	extInstanceID = "claworc-instance-id" // set only when access is authorized
	extDenyReason = "claworc-deny-reason" // denyMissingInstance | denyUnknownInstance
)

const (
	denyMissingInstance = "missing-instance"
	// denyUnknownInstance covers both "no such instance" and "not authorized"
	// so authenticated users cannot enumerate instance names.
	denyUnknownInstance = "unknown-instance"
)

var errAuthFailed = errors.New("unknown user or key")

func (g *Gateway) authenticate(cm ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	username, instancePart := ParseSSHUser(cm.User())
	if username == "" {
		return nil, errAuthFailed
	}

	user, err := database.GetUserByUsername(username)
	if err != nil {
		return nil, errAuthFailed
	}

	fingerprint := ssh.FingerprintSHA256(key)
	k, err := database.GetUserSSHKeyByFingerprint(fingerprint)
	if err != nil || k.UserID != user.ID {
		return nil, errAuthFailed
	}
	// Never trust the fingerprint alone: require byte equality with the
	// stored public key.
	storedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil || !bytes.Equal(storedKey.Marshal(), key.Marshal()) {
		return nil, errAuthFailed
	}

	g.limiter.RecordSuccess(hostOnly(cm.RemoteAddr()))
	database.TouchUserSSHKeyUsed(k.ID)

	perms := &ssh.Permissions{Extensions: map[string]string{
		extUserID:   strconv.FormatUint(uint64(user.ID), 10),
		extUsername: user.Username,
		extKeyID:    strconv.FormatUint(uint64(k.ID), 10),
	}}

	instanceID, denyReason := g.authorizeInstance(user, instancePart)
	if denyReason != "" {
		perms.Extensions[extDenyReason] = denyReason
	} else {
		perms.Extensions[extInstanceID] = strconv.FormatUint(uint64(instanceID), 10)
	}

	g.audit(sshaudit.EventGatewayLogin, instanceID, user.Username,
		fmt.Sprintf("remote=%s key=%s instance=%q deny=%q",
			hostOnly(cm.RemoteAddr()), fingerprint, instancePart, denyReason))
	return perms, nil
}

// authorizeInstance resolves the instance part of the login name and checks
// the user's access. Returns the instance ID on success, or a deny reason.
func (g *Gateway) authorizeInstance(user *database.User, instancePart string) (uint, string) {
	candidates := ResolveInstanceName(instancePart)
	if len(candidates) == 0 {
		return 0, denyMissingInstance
	}
	for _, name := range candidates {
		inst, err := database.GetInstanceByName(name)
		if err != nil {
			continue
		}
		if database.CanUserAccessInstance(user, inst.ID) {
			return inst.ID, ""
		}
		break // found but not authorized; render same as unknown
	}
	return 0, denyUnknownInstance
}

// accessibleInstanceNames lists the display-friendly instance names the user
// may connect to (capped), for the missing-instance help message.
func accessibleInstanceNames(userID uint, isAdmin bool) []string {
	const maxNames = 20
	var instances []database.Instance
	if isAdmin {
		if err := database.DB.Order("name").Limit(maxNames).Find(&instances).Error; err != nil {
			return nil
		}
	} else {
		ids, err := database.AccessibleInstanceIDs(userID)
		if err != nil || len(ids) == 0 {
			return nil
		}
		if err := database.DB.Where("id IN ?", ids).Order("name").Limit(maxNames).Find(&instances).Error; err != nil {
			return nil
		}
	}
	names := make([]string, 0, len(instances))
	for _, inst := range instances {
		names = append(names, strings.TrimPrefix(inst.Name, "bot-"))
	}
	sort.Strings(names)
	return names
}
