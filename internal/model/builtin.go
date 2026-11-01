package model

import (
	"path/filepath"
	"regexp"
	"strings"
)

// routerosPromptRe matches the interactive prompt RouterOS echoes after
// command output (e.g. "[admin@switch] > ").
var routerosPromptRe = regexp.MustCompile(`^\s*\[\S+@\S+\]\s*[>~]\s*$`)

// emptyLineRe matches blank lines, for models whose output has them.
var emptyLineRe = regexp.MustCompile(`^\s*$`)

func init() {
	// RouterOS: /export produces the full config; hide-sensitive includes
	// secrets (wireless keys, user passwords) that plain /export omits.
	// Strip the trailing prompt line(s) RouterOS echoes after output.
	Register("routeros", Spec{
		Commands:     []string{"/export hide-sensitive"},
		Strip:        []*regexp.Regexp{routerosPromptRe},
		CommandNames: []string{"export.rsc"},
	})

	// OPNsense (also works for pfSense): fetch the XML config directly.
	// Note: root's shell is csh — keep commands csh-compatible or wrap
	// them in `sh -c`.
	Register("opnsense", Spec{
		Files:     []string{"/conf/config.xml"},
		FileNames: []string{"config.xml"},
	})

	// Proxmox VE host: cluster config, storage, auth, network, boot.
	// Note: /etc/pve/priv/ is deliberately excluded as it holds secrets
	Register("proxmox", Spec{
		Files: []string{
			"/etc/pve/corosync.conf",
			"/etc/pve/storage.cfg",
			"/etc/pve/datacenter.cfg",
			"/etc/pve/user.cfg",
			"/etc/network/interfaces",
			"/etc/hosts",
			"/etc/fstab",
			"/etc/default/grub",
			// Globbed at fetch time — exact filenames vary across installs.
			"/etc/modprobe.d/*.conf",
		},
		FileNames: []string{
			"corosync.conf",
			"storage.cfg",
			"datacenter.cfg",
			"user.cfg",
			"interfaces",
			"hosts",
			"fstab",
			"grub-default",
			"", // glob matches are named by their relative path
		},
		SkipMissing: true, // corosync.conf only exists in clusters
	})

	// Pi-hole v6: FTL config dump (text) plus any dnsmasq drop-ins the
	// operator may have (globbed — filenames are install-specific; each
	// drop-in becomes its own verbatim artifact).
	// Teleporter zip comes later (binary, covers gravity DB).
	Register("pihole", Spec{
		Commands:     []string{"pihole-FTL --config"},
		CommandNames: []string{"ftl-config.txt"},
		Strip:        []*regexp.Regexp{emptyLineRe},
		Files:        []string{"/etc/dnsmasq.d/*"},
	})

	// Proxmox guest definitions (CTs and VMs), run on the PVE host.
	Register("proxmox-guests", Spec{
		Commands: []string{`for id in $(pct list | awk 'NR>1 {print $1}'); do
	echo "=== CT $id ==="
	pct config "$id"
	echo
done
for id in $(qm list | awk 'NR>1 {print $1}'); do
	echo "=== VM $id ==="
	qm config "$id"
	echo
done
`},
		CommandNames: []string{"guests.txt"},
	})

	// Generic: commands supplied per node via `commands:` config.
	Register("generic", Spec{})
}

// Generic builds a fetcher from per-node commands and files.
func Generic(commands, files []string) Fetcher {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = safeName(c)
	}
	fileNames := make([]string, len(files))
	for i, p := range files {
		fileNames[i] = filepath.Base(p)
	}
	return Spec{Commands: commands, CommandNames: names, Files: files, FileNames: fileNames}.Compile()
}

// safeName derives an artifact filename from a command string.
func safeName(cmd string) string {
	var b strings.Builder
	for _, r := range cmd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '.' || r == '=' || r == ':':
			b.WriteByte('-')
		default:
			// skip
		}
		if b.Len() >= maxNameRunes {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = fallbackArtifactName
	}
	return name + textArtifactExt
}
