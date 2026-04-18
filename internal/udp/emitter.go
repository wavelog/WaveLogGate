package udp

import (
	"fmt"
	"net"
)

// Emit sends data as a single UDP datagram to host:port.
func Emit(host string, port int, data string) error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(data))
	return err
}
