package phantomtcp

import (
	"crypto/tls"
	"encoding/binary"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

type DomainIP struct {
	Index     int
	Addresses []net.IP
}

var DNS string = ""
var ACache sync.Map
var AAAACache sync.Map
var Nose []string = []string{"phantom.socks"}
var NoseLock sync.Mutex

func TCPlookup(request []byte, address string) ([]byte, error) {
	data := make([]byte, 1024)
	binary.BigEndian.PutUint16(data[:2], uint16(len(request)))
	copy(data[2:], request)

	var conn net.Conn
	var err error
	if false {
	} else {
		conn, err = net.DialTimeout("tcp", address, time.Second*5)
		if err != nil {
			return nil, err
		}

		defer conn.Close()

		_, err = conn.Write(data[:len(request)+2])
		if err != nil {
			return nil, err
		}
	}

	length := 0
	recvlen := 0
	for {
		if recvlen >= 1024 {
			return nil, nil
		}
		n, err := conn.Read(data[recvlen:])
		if err != nil {
			return nil, err
		}
		if length == 0 {
			length = int(binary.BigEndian.Uint16(data[:2]) + 2)
		}
		recvlen += n
		if recvlen >= length {
			return data[2:recvlen], nil
		}
	}
}

func TCPlookupDNS64(request []byte, address string, offset int, prefix []byte) ([]byte, error) {
	response6 := make([]byte, 1024)
	offset6 := offset
	offset4 := offset

	binary.BigEndian.PutUint16(request[offset-4:offset-2], 1)
	response, err := TCPlookup(request, address)
	if err != nil {
		return nil, err
	}

	copy(response6, response[:offset])
	binary.BigEndian.PutUint16(response6[offset-4:offset-2], 28)

	count := int(binary.BigEndian.Uint16(response[6:8]))
	for i := 0; i < count; i++ {
		for {
			if offset >= len(response) {
				log.Println(offset)
				return nil, nil
			}
			length := response[offset]
			offset++
			if length == 0 {
				break
			}
			if length < 63 {
				offset += int(length)
				if offset+2 > len(response) {
					log.Println(offset)
					return nil, nil
				}
			} else {
				offset++
				break
			}
		}
		if offset+2 > len(response) {
			log.Println(offset)
			return nil, nil
		}

		copy(response6[offset6:], response[offset4:offset])
		offset6 += offset - offset4
		offset4 = offset

		AType := binary.BigEndian.Uint16(response[offset : offset+2])
		offset += 8
		if offset+2 > len(response) {
			log.Println(offset)
			return nil, nil
		}
		DataLength := binary.BigEndian.Uint16(response[offset : offset+2])
		offset += 2

		offset += int(DataLength)
		if AType == 1 {
			if offset > len(response) {
				log.Println(offset)
				return nil, nil
			}
			binary.BigEndian.PutUint16(response6[offset6:], 28)
			offset6 += 2
			offset4 += 2
			copy(response6[offset6:], response[offset4:offset4+6])
			offset6 += 6
			offset4 += 6
			binary.BigEndian.PutUint16(response6[offset6:], DataLength+12)
			offset6 += 2
			offset4 += 2

			copy(response6[offset6:], prefix[:12])
			offset6 += 12
			copy(response6[offset6:], response[offset4:offset])
			offset6 += offset - offset4
			offset4 = offset
		} else {
			copy(response6[offset6:], response[offset4:offset])
			offset6 += offset - offset4
			offset4 = offset
		}
	}

	return response6[:offset6], nil
}

func UDPlookup(request []byte, address string) ([]byte, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_, err = conn.Write(request)
	if err != nil {
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	response := make([]byte, 1024)

	if request[11] == 0 {
		n, err := conn.Read(response[:])
		return response[:n], err
	} else {
		var n int
		for {
			n, err = conn.Read(response[:])
			if err != nil {
				return nil, err
			}

			if request[11] == 0 || response[11] > 0 {
				break
			}
		}
		return response[:n], nil
	}
}

func TLSlookup(request []byte, address string) ([]byte, error) {
	conf := &tls.Config{
		InsecureSkipVerify: true,
	}
	conn, err := tls.Dial("tcp", address, conf)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	data := make([]byte, 1024)
	binary.BigEndian.PutUint16(data[:2], uint16(len(request)))
	copy(data[2:], request)

	_, err = conn.Write(data[:len(request)+2])
	if err != nil {
		return nil, err
	}

	length := 0
	recvlen := 0
	for {
		n, err := conn.Read(data[recvlen:])
		if err != nil {
			return nil, err
		}
		if length == 0 {
			length = int(binary.BigEndian.Uint16(data[:2]) + 2)
		}
		recvlen += n
		if recvlen >= length {
			return data[2:recvlen], nil
		}
	}
}

func GetQName(buf []byte) (string, int, int) {
	bufflen := len(buf)
	if bufflen < 13 {
		return "", 0, 0
	}
	length := buf[12]
	off := 13
	end := off + int(length)
	qname := string(buf[off:end])
	off = end

	for {
		if off >= bufflen {
			return "", 0, 0
		}
		length := buf[off]
		off++
		if length == 0x00 {
			break
		}
		end := off + int(length)
		if end > bufflen {
			return "", 0, 0
		}
		qname += "." + string(buf[off:end])
		off = end
	}
	end = off + 4
	if end > bufflen {
		return "", 0, 0
	}

	qtype := int(binary.BigEndian.Uint16(buf[off : off+2]))

	return qname, qtype, end
}

func GetName(buf []byte, offset int) (string, int) {
	name := ""
	for {
		length := int(buf[offset])
		offset++
		if length == 0 {
			break
		}
		if name != "" {
			name += "."
		}
		if length < 63 {
			name += string(buf[offset : offset+length])
			offset += int(length)
			if offset+2 > len(buf) {
				return "", offset
			}
		} else {
			_offset := int(buf[offset])
			_name, _ := GetName(buf, _offset)
			name += _name
			return name, offset + 1
		}
	}
	return name, offset
}

func GetNameOffset(response []byte, offset int) int {
	responseLen := len(response)

	for {
		if offset >= responseLen {
			return 0
		}
		length := response[offset]
		offset++
		if length == 0 {
			break
		}
		if length < 63 {
			offset += int(length)
			if offset+2 > responseLen {
				return 0
			}
		} else {
			offset++
			break
		}
	}

	return offset
}

func getAnswers(response []byte) []net.IP {
	responseLen := len(response)

	offset := 12
	if offset > responseLen {
		return nil
	}

	QDCount := int(binary.BigEndian.Uint16(response[4:6]))
	ANCount := int(binary.BigEndian.Uint16(response[6:8]))

	if ANCount == 0 {
		return nil
	}

	for i := 0; i < QDCount; i++ {
		_offset := GetNameOffset(response, offset)
		if _offset == 0 {
			return nil
		}
		offset = _offset + 4
	}

	ips := make([]net.IP, 0)
	cname := ""
	for i := 0; i < ANCount; i++ {
		_offset := GetNameOffset(response, offset)
		if _offset == 0 {
			return nil
		}
		offset = _offset
		if offset+2 > responseLen {
			return nil
		}
		AType := binary.BigEndian.Uint16(response[offset : offset+2])
		offset += 8
		if offset+2 > responseLen {
			return nil
		}
		DataLength := binary.BigEndian.Uint16(response[offset : offset+2])
		offset += 2

		if AType == 1 {
			if offset+4 > responseLen {
				return nil
			}
			data := response[offset : offset+4]
			ip := net.IPv4(data[0], data[1], data[2], data[3])
			ips = append(ips, ip)
		} else if AType == 28 {
			var data [16]byte
			if offset+16 > responseLen {
				return nil
			}
			copy(data[:], response[offset:offset+16])
			ip := net.IP(response[offset : offset+16])
			ips = append(ips, ip)
		} else if AType == 5 {
			cname, _ = GetName(response, offset)
			logPrintln(4, "CNAME:", cname)
		}

		offset += int(DataLength)
	}

	//if len(ips) == 0 && cname != "" {
	//	_, ips = NSLookup(cname, qtype)
	//}

	return ips
}

func packAnswers(ips []net.IP, qtype int) (int, []byte) {
	totalLen := 0
	count := 0
	for _, ip := range ips {
		ip4 := ip.To4()
		if ip4 != nil {
			if qtype == 1 {
				count++
				totalLen += 16
			}
		} else {
			if qtype == 28 {
				count++
				totalLen += 28
			}
		}
	}

	answers := make([]byte, totalLen)
	length := 0
	for _, ip := range ips {
		ip4 := ip.To4()
		if ip4 != nil {
			if qtype == 1 {
				answer := []byte{0xC0, 0x0C, 0x00, 1,
					0x00, 0x01, 0x00, 0x00, 0x0E, 0x10, 0x00, 0x04,
					ip4[0], ip4[1], ip4[2], ip4[3]}
				copy(answers[length:], answer)
				length += 16
			}
		} else {
			if qtype == 28 {
				answer := []byte{0xC0, 0x0C, 0x00, 28,
					0x00, 0x01, 0x00, 0x00, 0x0E, 0x10, 0x00, 0x10}
				copy(answers[length:], answer)
				length += 12
				copy(answers[length:], ip)
				length += 16
			}
		}
	}

	return count, answers
}

func BuildResponse(request []byte, ips []net.IP, qtype int) []byte {
	response := make([]byte, 1024)
	copy(response, request)
	length := len(request)
	response[2] = 0x81
	response[3] = 0x80

	if len(ips) == 0 {
		return response[:length]
	}

	count, answer := packAnswers(ips, qtype)
	binary.BigEndian.PutUint16(response[6:], uint16(count))
	if count > 0 {
		copy(response[length:], answer)
		length += len(answer)
	}

	return response[:length]
}

func BuildLie(request []byte, id int, qtype int) []byte {
	response := make([]byte, 1024)
	copy(response, request)
	length := len(request)
	response[2] = 0x81
	response[3] = 0x80
	if qtype == 1 {
		answer := []byte{0xC0, 0x0C, 0x00, 1,
			0x00, 0x01, 0x00, 0x00, 0x00, 0x10, 0x00, 0x04,
			6, 0}
		copy(response[length:], answer)
		length += 14
		binary.BigEndian.PutUint16(response[length:], uint16(id))
		length += 2
		binary.BigEndian.PutUint16(response[6:], 1)
	} else if qtype == 28 {
		answer := []byte{0xC0, 0x0C, 0x00, 28,
			0x00, 0x01, 0x00, 0x00, 0x00, 0x10, 0x00, 0x10,
			0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00}
		copy(response[length:], answer)
		length += 24
		binary.BigEndian.PutUint32(response[length:], uint32(id))
		length += 4
		binary.BigEndian.PutUint16(response[6:], 1)
	}
	return response[:length]
}

func PackQName(name string) []byte {
	length := strings.Count(name, "")
	QName := make([]byte, length+1)
	copy(QName[1:], []byte(name))
	o, l := 0, 0
	for i := 1; i < length; i++ {
		if QName[i] == '.' {
			QName[o] = byte(l)
			l = 0
			o = i
		} else {
			l++
		}
	}
	QName[o] = byte(l)

	return QName
}

type ServerOptions struct {
	ECS  string
	Type string
	PD   string
}

func ParseOptions(options string) ServerOptions {
	opts := strings.Split(options, "&")
	var serverOpts ServerOptions
	for _, opt := range opts {
		key := strings.SplitN(opt, "=", 2)
		if len(key) > 1 {
			switch key[0] {
			case "ecs":
				serverOpts.ECS = key[1]
			case "pd":
				serverOpts.PD = key[1]
			case "type":
				serverOpts.Type = key[1]
			}
		}
	}

	return serverOpts
}

func PackRequest(name string, qtype uint16, ecs string) []byte {
	Request := make([]byte, 512)

	binary.BigEndian.PutUint16(Request[:], 0)       //ID
	binary.BigEndian.PutUint16(Request[2:], 0x0100) //Flag
	binary.BigEndian.PutUint16(Request[4:], 1)      //QDCount
	binary.BigEndian.PutUint16(Request[6:], 0)      //ANCount
	binary.BigEndian.PutUint16(Request[8:], 0)      //NSCount
	if ecs != "" {
		binary.BigEndian.PutUint16(Request[10:], 1) //ARCount
	} else {
		binary.BigEndian.PutUint16(Request[10:], 0) //ARCount
	}

	qname := PackQName(name)
	length := len(qname)
	copy(Request[12:], qname)
	length += 12
	binary.BigEndian.PutUint16(Request[length:], qtype)
	length += 2
	binary.BigEndian.PutUint16(Request[length:], 0x01) //QClass
	length += 2

	if ecs != "" {
		Request[length] = 0 //Name
		length++
		binary.BigEndian.PutUint16(Request[length:], 41) // Type
		length += 2
		binary.BigEndian.PutUint16(Request[length:], 4096) // UDP Payload
		length += 2
		Request[length] = 0 // Highter bits in extended RCCODE
		length++
		Request[length] = 0 // EDNS0 Version
		length++
		binary.BigEndian.PutUint16(Request[length:], 0x800) // Z
		length += 2

		ecsip := net.ParseIP(ecs)
		ecsip4 := ecsip.To4()
		if ecsip4 != nil {
			binary.BigEndian.PutUint16(Request[length:], 11) // Length
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 8) // Option Code
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 7) // Option Length
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 1) // Family
			length += 2
			Request[length] = 24 // Source Netmask
			length++
			Request[length] = 0 // Scope Netmask
			length++
			copy(Request[length:], ecsip4[:3])
			length += 3
		} else {
			binary.BigEndian.PutUint16(Request[length:], 15) // Length
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 8) // Option Code
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 11) // Option Length
			length += 2
			binary.BigEndian.PutUint16(Request[length:], 2) // Family
			length += 2
			Request[length] = 56 // Source Netmask
			length++
			Request[length] = 0 // Scope Netmask
			length++
			copy(Request[length:], ecsip[:7])
			length += 7
		}
	}

	return Request[:length]
}

func LoadDNSCache(qname string, qtype uint16) (DomainIP, bool) {
	var ok bool
	var result interface{}
	var answer DomainIP = DomainIP{0, nil}
	switch qtype {
	case 1:
		result, ok = ACache.Load(qname)
	case 28:
		result, ok = AAAACache.Load(qname)
	default:
		return answer, false
	}

	if ok {
		answer = result.(DomainIP)
	}

	return answer, ok
}

func StoreDNSCache(qname string, qtype uint16, answer DomainIP) {
	switch qtype {
	case 1:
		ACache.Store(qname, answer)
	case 28:
		AAAACache.Store(qname, answer)
	}
}

func NSLookup(name string, qtype uint16, server string) (int, []net.IP) {
	if qtype != 1 && qtype != 28 {
		return 0, nil
	}

	answer, ok := LoadDNSCache(name, qtype)
	if ok {
		logPrintln(3, "cached:", name, qtype, answer.Addresses)
		return answer.Index, answer.Addresses
	}
	offset := 0
	for i := 0; i < SubdomainDepth; i++ {
		off := strings.Index(name[offset:], ".")
		if off == -1 {
			break
		}
		offset += off
		answer, ok := LoadDNSCache(name[offset:], qtype)

		if ok {
			logPrintln(3, "cached:", name, qtype, i, answer.Addresses)
			return answer.Index, answer.Addresses
		}
		offset++
	}

	var request []byte
	var response []byte
	var err error

	_server := strings.SplitN(server, "/", 4)

	var options ServerOptions
	if len(_server) > 3 {
		options = ParseOptions(_server[3])
	}

	if len(_server) > 2 {
		switch _server[0] {
		case "udp:":
			request = PackRequest(name, qtype, options.ECS)
			response, err = UDPlookup(request, _server[2])
		case "tcp:":
			request = PackRequest(name, qtype, options.ECS)
			response, err = TCPlookup(request, _server[2])
		case "tls:":
			request = PackRequest(name, qtype, options.ECS)
			response, err = TLSlookup(request, _server[2])
		default:
			NoseLock.Lock()
			index := len(Nose)
			Nose = append(Nose, name)
			NoseLock.Unlock()
			StoreDNSCache(name, 1, DomainIP{index, nil})
			StoreDNSCache(name, 28, DomainIP{0, nil})
			return index, nil
		}
	}
	if err != nil {
		logPrintln(1, err)
		return 0, nil
	}

	ips := getAnswers(response)

	if options.PD != "" {
		for i, ip := range ips {
			ips[i] = net.ParseIP(options.PD + ip.String())
		}
	}
	logPrintln(3, name, qtype, ips)

	NoseLock.Lock()
	index := len(Nose)
	Nose = append(Nose, name)
	NoseLock.Unlock()
	StoreDNSCache(name, qtype, DomainIP{index, ips})

	return index, ips
}

func NSRequest(request []byte) []byte {
	name, qtype, _ := GetQName(request)
	if name == "" {
		logPrintln(2, "DNS Segmentation fault")
		return nil
	}

	if qtype != 1 && qtype != 28 {
		return BuildResponse(request, nil, qtype)
	}

	answer, ok := LoadDNSCache(name, uint16(qtype))
	if ok {
		if answer.Index > 0 {
			return BuildLie(request, answer.Index, qtype)
		} else {
			return BuildResponse(request, answer.Addresses, qtype)
		}
	}
	offset := 0
	for i := 0; i < SubdomainDepth; i++ {
		off := strings.Index(name[offset:], ".")
		if off == -1 {
			break
		}
		offset += off
		answer, ok := LoadDNSCache(name[offset:], uint16(qtype))
		if ok {
			logPrintln(3, "cached:", name, qtype, answer.Addresses)
			if answer.Index > 0 {
				return BuildLie(request, answer.Index, qtype)
			} else {
				return BuildResponse(request, answer.Addresses, qtype)
			}
		}
		offset++
	}

	var response []byte
	var err error

	conf, ok := ConfigLookup(name)
	var options ServerOptions
	var method uint32
	var serverAddr []string
	if ok {
		method = conf.Option
		logPrintln(2, name, conf.Server)
		serverAddr = strings.SplitN(conf.Server, "/", 4)
	} else {
		method = 0
		logPrintln(2, name, DNS)
		serverAddr = strings.SplitN(DNS, "/", 4)
	}

	if len(serverAddr) > 3 {
		options = ParseOptions(serverAddr[3])
	}

	if options.Type == "A" && qtype == 28 {
		return BuildResponse(request, nil, qtype)
	} else if options.Type == "AAAA" && qtype == 1 {
		return BuildResponse(request, nil, qtype)
	}

	if len(serverAddr) > 2 {
		if method != 0 {
			if qtype == 28 {
				return BuildResponse(request, nil, qtype)
			}
			_qtype := uint16(qtype)
			if method&OPT_IPV6 != 0 {
				_qtype = 28
			}
			switch serverAddr[0] {
			case "udp:":
				request = PackRequest(name, _qtype, options.ECS)
				response, err = UDPlookup(request, serverAddr[2])
			case "tcp:":
				request = PackRequest(name, _qtype, options.ECS)
				response, err = TCPlookup(request, serverAddr[2])
			case "tls:":
				request = PackRequest(name, _qtype, options.ECS)
				response, err = TLSlookup(request, serverAddr[2])
			default:
				NoseLock.Lock()
				index := len(Nose)
				Nose = append(Nose, name)
				NoseLock.Unlock()
				StoreDNSCache(name, 1, DomainIP{index, nil})
				StoreDNSCache(name, 28, DomainIP{0, nil})
				return BuildLie(request, index, qtype)
			}
		} else {
			switch serverAddr[0] {
			case "udp:":
				response, err = UDPlookup(request, serverAddr[2])
			case "tcp:":
				response, err = TCPlookup(request, serverAddr[2])
			case "tls:":
				response, err = TLSlookup(request, serverAddr[2])
			default:
				return nil
			}
		}
	}
	if err != nil {
		logPrintln(1, err)
		return nil
	}

	ips := getAnswers(response)
	logPrintln(3, name, qtype, ips)

	if options.PD != "" {
		for i, ip := range ips {
			ips[i] = net.ParseIP(options.PD + ip.String())
		}

		if method != 0 {
			if qtype == 28 {
				return BuildResponse(request, nil, qtype)
			}
			NoseLock.Lock()
			index := len(Nose)
			Nose = append(Nose, name)
			NoseLock.Unlock()
			StoreDNSCache(name, 1, DomainIP{index, ips})
			StoreDNSCache(name, 28, DomainIP{0, nil})
			return BuildLie(request, index, qtype)
		} else {
			StoreDNSCache(name, uint16(qtype), DomainIP{0, ips})
			response = BuildResponse(request, ips, qtype)
		}
	} else {
		index := 0
		if method != 0 {
			NoseLock.Lock()
			index = len(Nose)
			Nose = append(Nose, name)
			NoseLock.Unlock()
			return BuildLie(request, index, qtype)
		}
		StoreDNSCache(name, uint16(qtype), DomainIP{index, ips})
	}

	return response
}
