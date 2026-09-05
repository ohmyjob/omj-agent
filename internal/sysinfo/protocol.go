package sysinfo

import (
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/version"
)

// WorkMetadata never yields a nil address list because the Server requires
// the field and a nil slice would encode as null. runAsAllowed comes from
// config.ResolveRunAs, the only producer of that list.
func (i Info) WorkMetadata(insecureHTTP bool, runAsAllowed []string) protocol.MachineMetadata {
	ips := i.ReportedIPs
	if ips == nil {
		ips = []string{}
	}

	return protocol.MachineMetadata{
		Hostname:      i.Hostname,
		OS:            i.OS,
		OSVersion:     i.OSVersion,
		Arch:          i.Arch,
		KernelVersion: i.KernelVersion,
		AgentUser:     i.AgentUser,
		AgentUID:      i.AgentUID,
		InsecureHTTP:  insecureHTTP,
		ReportedIPs:   ips,
		RunAsAllowed:  runAsAllowed,
	}
}

func (i Info) EnrollRequest(token, name string, insecureHTTP bool, runAsAllowed []string) protocol.EnrollRequest {
	request := protocol.EnrollRequest{
		Token:           token,
		MachineMetadata: i.WorkMetadata(insecureHTTP, runAsAllowed),
		AgentVersion:    version.Version,
	}

	if name != "" {
		request.Name = &name
	}

	return request
}
