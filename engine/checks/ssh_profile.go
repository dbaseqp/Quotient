package checks

import (
	"errors"
	"hash/fnv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshClientProfile is the identity the SSH check presents to a team's server:
// the version banner of a real OpenSSH client build and the KEXINIT name-lists
// that build sends with its default configuration, copied from a capture of
// that client (see ssh_profile_test.go).
//
// The lists are kept exactly as the real client sends them. x/crypto/ssh drops
// the entries it does not implement (Config.SetDefaults), so the KEXINIT on the
// wire is the real one minus sshUnimplementedAlgos. kex excludes the
// "ext-info-c,kex-strict-c-v00@openssh.com" tail, which x/crypto/ssh appends
// itself in the same order as OpenSSH; every profile is a build that sends
// both markers.
type sshClientProfile struct {
	banner       string
	kex          []string
	ciphers      []string
	macs         []string
	hostKeyAlgos []string
}

var (
	opensshCiphers = []string{
		"chacha20-poly1305@openssh.com",
		"aes128-ctr", "aes192-ctr", "aes256-ctr",
		"aes128-gcm@openssh.com", "aes256-gcm@openssh.com",
	}
	opensshMACs = []string{
		"umac-64-etm@openssh.com", "umac-128-etm@openssh.com",
		"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com",
		"hmac-sha1-etm@openssh.com",
		"umac-64@openssh.com", "umac-128@openssh.com",
		"hmac-sha2-256", "hmac-sha2-512", "hmac-sha1",
	}
	// OpenSSH 8.8+ host key list (no ssh-rsa), as sent with an empty known_hosts.
	opensshHostKeyAlgos = []string{
		"ssh-ed25519-cert-v01@openssh.com",
		"ecdsa-sha2-nistp256-cert-v01@openssh.com", "ecdsa-sha2-nistp384-cert-v01@openssh.com", "ecdsa-sha2-nistp521-cert-v01@openssh.com",
		"sk-ssh-ed25519-cert-v01@openssh.com", "sk-ecdsa-sha2-nistp256-cert-v01@openssh.com",
		"rsa-sha2-512-cert-v01@openssh.com", "rsa-sha2-256-cert-v01@openssh.com",
		"ssh-ed25519",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com",
		"rsa-sha2-512", "rsa-sha2-256",
	}
	// OpenSSH 8.5-9.8 default key exchange list.
	opensshSntrupKex = []string{
		"sntrup761x25519-sha512@openssh.com",
		"curve25519-sha256", "curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
		"diffie-hellman-group-exchange-sha256",
		"diffie-hellman-group16-sha512", "diffie-hellman-group18-sha512",
		"diffie-hellman-group14-sha256",
	}
)

// sshClientProfiles are the client builds the check can pose as. Only builds
// that send kex-strict-c-v00@openssh.com qualify, because x/crypto/ssh always
// sends it: OpenSSH 9.6+ and distribution builds with the CVE-2023-48795
// backport.
var sshClientProfiles = []sshClientProfile{
	{ // Ubuntu 24.04, openssh-client 1:9.6p1-3ubuntu13.20
		banner:       "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.20",
		kex:          opensshSntrupKex,
		ciphers:      opensshCiphers,
		macs:         opensshMACs,
		hostKeyAlgos: opensshHostKeyAlgos,
	},
	{ // Debian 12, openssh-client 1:9.2p1-2+deb12u10 (Debian backported the sntrup761x25519-sha512 alias)
		banner:       "SSH-2.0-OpenSSH_9.2p1 Debian-2+deb12u10",
		kex:          append([]string{"sntrup761x25519-sha512"}, opensshSntrupKex...),
		ciphers:      opensshCiphers,
		macs:         opensshMACs,
		hostKeyAlgos: opensshHostKeyAlgos,
	},
}

// sshUnimplementedAlgos are algorithms the profiles advertise that
// x/crypto/ssh does not implement. They are left out of the KEXINIT on the
// wire, which is why it still differs from the real client's (HASSH included).
var sshUnimplementedAlgos = []string{
	"sntrup761x25519-sha512", "sntrup761x25519-sha512@openssh.com",
	"diffie-hellman-group18-sha512",
	"umac-64-etm@openssh.com", "umac-128-etm@openssh.com",
	"hmac-sha1-etm@openssh.com",
	"umac-64@openssh.com", "umac-128@openssh.com",
}

// sshProfileFor picks the client identity for a target. The choice is stable
// per target, so a team's server always sees the same client version, as it
// would from one administrator's workstation.
func sshProfileFor(target string) sshClientProfile {
	h := fnv.New32a()
	_, _ = h.Write([]byte(target))
	return sshClientProfiles[h.Sum32()%uint32(len(sshClientProfiles))]
}

// clientConfig returns a config that advertises this profile.
func (p sshClientProfile) clientConfig(user string, auth []ssh.AuthMethod, timeout time.Duration) *ssh.ClientConfig {
	config := &ssh.ClientConfig{
		User:              user,
		Auth:              auth,
		HostKeyCallback:   ssh.InsecureIgnoreHostKey(), // #nosec G106 -- competition hosts have unknown keys
		Timeout:           timeout,
		ClientVersion:     p.banner,
		HostKeyAlgorithms: p.hostKeyAlgos,
	}
	config.KeyExchanges = p.kex
	config.Ciphers = p.ciphers
	config.MACs = p.macs
	return config
}

// legacyConfig returns the config the check used before profiles existed:
// x/crypto/ssh defaults, plus 3des-cbc for the scored login (bad attempts
// never appended it). It is only used when the server shares no algorithm
// with the profile, so servers that only speak old algorithms score exactly
// as before.
func (p sshClientProfile) legacyConfig(user string, auth []ssh.AuthMethod, timeout time.Duration, with3DES bool) *ssh.ClientConfig {
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- competition hosts have unknown keys
		Timeout:         timeout,
		ClientVersion:   p.banner,
	}
	if with3DES {
		config.SetDefaults()
		config.Ciphers = append(config.Ciphers, "3des-cbc")
	}
	return config
}

// dial connects with the profile's algorithms and retries once with the legacy
// set only when negotiation finds no common algorithm. Any other failure
// (refused, hang-up after KEXINIT, authentication) is returned as is: a retry
// there would let a server that drops the first connection make the check
// reveal the old Go fingerprint, and would double failed logins.
func (p sshClientProfile) dial(addr, user string, auth []ssh.AuthMethod, timeout time.Duration, with3DES bool) (*ssh.Client, error) {
	conn, err := ssh.Dial("tcp", addr, p.clientConfig(user, auth, timeout))
	if err == nil || !strings.Contains(err.Error(), "no common algorithm") {
		return conn, err
	}
	return ssh.Dial("tcp", addr, p.legacyConfig(user, auth, timeout, with3DES))
}

// opensshPtyModes is the terminal-mode list OpenSSH sends from a Linux
// terminal at its default settings, in OpenSSH's order, except that ECHO is
// off as the check always had it. The values equal a Linux pty's defaults, so
// the remote terminal behaves as it did with the old three-entry list; only
// the request now has OpenSSH's shape and size.
var opensshPtyModes = []struct {
	op  byte
	val uint32
}{
	{ssh.TTY_OP_OSPEED, 38400}, {ssh.TTY_OP_ISPEED, 38400},
	{ssh.VINTR, 3}, {ssh.VQUIT, 28}, {ssh.VERASE, 127}, {ssh.VKILL, 21}, {ssh.VEOF, 4},
	{ssh.VEOL, 255}, {ssh.VEOL2, 255}, {ssh.VSTART, 17}, {ssh.VSTOP, 19}, {ssh.VSUSP, 26},
	{ssh.VREPRINT, 18}, {ssh.VWERASE, 23}, {ssh.VLNEXT, 22}, {ssh.VDISCARD, 15},
	{ssh.IGNPAR, 0}, {ssh.PARMRK, 0}, {ssh.INPCK, 0}, {ssh.ISTRIP, 0}, {ssh.INLCR, 0},
	{ssh.IGNCR, 0}, {ssh.ICRNL, 1}, {ssh.IUCLC, 0}, {ssh.IXON, 1}, {ssh.IXANY, 0},
	{ssh.IXOFF, 0}, {ssh.IMAXBEL, 0}, {ssh.IUTF8, 0},
	{ssh.ISIG, 1}, {ssh.ICANON, 1}, {ssh.XCASE, 0}, {ssh.ECHO, 0}, {ssh.ECHOE, 1},
	{ssh.ECHOK, 1}, {ssh.ECHONL, 0}, {ssh.NOFLSH, 0}, {ssh.TOSTOP, 0}, {ssh.IEXTEN, 1},
	{ssh.ECHOCTL, 1}, {ssh.ECHOKE, 1}, {ssh.PENDIN, 0},
	{ssh.OPOST, 1}, {ssh.OLCUC, 0}, {ssh.ONLCR, 1}, {ssh.OCRNL, 0}, {ssh.ONOCR, 0}, {ssh.ONLRET, 0},
	{ssh.CS7, 1}, {ssh.CS8, 1}, {ssh.PARENB, 0}, {ssh.PARODD, 0},
}

// requestOpenSSHPty asks for the same 80x40 "xterm" terminal the check always
// used, encoded the way OpenSSH encodes it: zero pixel size (Session.RequestPty
// sends 8 pixels per cell) and the full mode list in OpenSSH's order.
func requestOpenSSHPty(session *ssh.Session) error {
	modes := make([]byte, 0, len(opensshPtyModes)*5+1)
	for _, m := range opensshPtyModes {
		modes = append(modes, m.op, byte(m.val>>24), byte(m.val>>16), byte(m.val>>8), byte(m.val))
	}
	modes = append(modes, 0) // TTY_OP_END
	req := struct {
		Term          string
		Columns, Rows uint32
		Width, Height uint32
		Modes         string
	}{"xterm", 80, 40, 0, 0, string(modes)}
	ok, err := session.SendRequest("pty-req", true, ssh.Marshal(&req))
	if err == nil && !ok {
		err = errors.New("ssh: pty-req failed")
	}
	return err
}
