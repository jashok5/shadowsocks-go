package runtime

import (
	"net"
	"strconv"
	"strings"
)

type udpAssocKeyMode string

const (
	udpAssocKeyClientOnly   udpAssocKeyMode = "client_only"
	udpAssocKeyClientTarget udpAssocKeyMode = "client_target"
	udpAssocKeyClientUID    udpAssocKeyMode = "client_uid"
)

func buildUDPAssocKey(mode udpAssocKeyMode, clientAddr net.Addr, target string, uid int) string {
	client := ""
	if clientAddr != nil {
		client = strings.TrimSpace(clientAddr.String())
	}
	target = strings.TrimSpace(target)
	switch mode {
	case udpAssocKeyClientTarget:
		return client + "|" + target
	case udpAssocKeyClientUID:
		return client + "|" + strconv.Itoa(uid)
	case udpAssocKeyClientOnly:
		fallthrough
	default:
		return client
	}
}
