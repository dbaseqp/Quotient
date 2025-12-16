package api

import (
	"net/http"
	"quotient/engine/checks"
)

type BoxMetadata struct {
	Name     string   `json:"name"`
	IP       string   `json:"ip"`
	Services []string `json:"services"`
}

type MetadataResponse struct {
	Boxes []BoxMetadata `json:"boxes"`
}

func GetMetadata(w http.ResponseWriter, r *http.Request) {
	if !CheckCompetitionStarted(w, r) {
		return
	}

	WriteJSON(w, http.StatusOK, buildMetadata())
}

func buildMetadata() MetadataResponse {
	var metadata MetadataResponse

	for _, box := range conf.Box {
		boxMeta := BoxMetadata{
			Name:     box.Name,
			IP:       box.IP,
			Services: []string{},
		}

		boxMeta.Services = append(boxMeta.Services, extractServices(box.Custom, "custom")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Dns, "dns")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Ftp, "ftp")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Imap, "imap")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Ldap, "ldap")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Ping, "ping")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Pop3, "pop3")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Rdp, "rdp")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Smb, "smb")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Smtp, "smtp")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Sql, "sql")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Ssh, "ssh")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Tcp, "tcp")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Vnc, "vnc")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.Web, "web")...)
		boxMeta.Services = append(boxMeta.Services, extractServices(box.WinRM, "winrm")...)

		metadata.Boxes = append(metadata.Boxes, boxMeta)
	}

	return metadata
}

func extractServices[T checks.Runner](services []T, serviceType string) []string {
	var result []string

	for _, service := range services {
		var displayName string

		if svc, ok := interface{}(service).(*checks.Custom); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Dns); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Ftp); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Imap); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Ldap); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Ping); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Pop3); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Rdp); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Smb); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Smtp); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Sql); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Ssh); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Tcp); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Vnc); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.Web); ok {
			displayName = svc.Display
		} else if svc, ok := interface{}(service).(*checks.WinRM); ok {
			displayName = svc.Display
		}

		if displayName != "" {
			result = append(result, displayName)
		}
	}

	return result
}
