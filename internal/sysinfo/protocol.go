package sysinfo

import (
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/version"
)

// WorkMetadata never yields a nil address list because the Server requires
// the field and a nil slice would encode as null.
func (i Info) WorkMetadata(insecureHTTP bool) protocol.MachineMetadata {
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
	}
}

func (i Info) EnrollRequest(token, name string, insecureHTTP bool) protocol.EnrollRequest {
	request := protocol.EnrollRequest{
		Token:           token,
		MachineMetadata: i.WorkMetadata(insecureHTTP),
		AgentVersion:    version.Version,
	}

	if name != "" {
		request.Name = &name
	}

	return request
}
