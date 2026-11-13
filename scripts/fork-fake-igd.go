//go:build ignore

// Fork addition: a stand-in for a home router's UPnP service (an Internet
// Gateway Device), for trying the router port forwarding (src/upnp) without
// a router. Used with scripts/fork-ui-check-upnp.mjs.
//
// It answers SSDP searches on UDP 1900, serves a device description and a
// WANIPConnection control URL on :5000 backed by a table of port mappings,
// and has a plain control port (:8080) for whoever runs the test:
//
//	GET /table                      the mappings, as JSON
//	GET /put?key=TCP/8096&client=10.0.0.9&port=8096&desc=NAS
//	                                a mapping as another device would have made
//	GET /hide?key=TCP/443&code=718  a forward made in the router's own pages:
//	                                not in the table, asked for it the router
//	                                answers that error (0: "done", no effect)
//	GET /drop?key=TCP/8096          the lease of a mapping ends
//	GET /reboot                     every mapping is gone
//	GET /down?set=1                 stop answering (0: answer again)
//	GET /calls                      what was asked, in order
//
// Like miniupnpd it updates a mapping its own client asks for again and
// refuses one another client holds with 718. The public address it reports
// is 203.0.113.7 (a documentation address).
//
// How it is used: Cosmos' client only ever talks to the machine's default
// gateway. So this program runs in a container with a fixed address on a
// Docker network of the test's own, and the throwaway Cosmos runs on the
// same network with --cap-add NET_ADMIN and its default route replaced:
// "ip route replace default via <this container>". Nothing on the host
// changes. It is not part of any build (go run scripts/fork-fake-igd.go).
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type forward struct {
	Client      string `json:"client"`
	Port        string `json:"port"`
	Description string `json:"description"`
	Lease       string `json:"lease"`
}

var (
	mu     sync.Mutex
	table  = map[string]*forward{}
	hidden = map[string]int{}
	calls  = []string{}
	down   bool
)

const service = "urn:schemas-upnp-org:service:WANIPConnection:1"

func ownAddress() string {
	if ip := os.Getenv("IGD_IP"); ip != "" {
		return ip
	}
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if network, ok := addr.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
			return network.IP.String()
		}
	}
	return "127.0.0.1"
}

func leaves(doc []byte) map[string]string {
	values := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var name string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return values
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name = t.Name.Local
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if name == t.Name.Local {
				values[name] = strings.TrimSpace(text.String())
			}
			name = ""
		}
	}
}

func keys() []string {
	list := make([]string, 0, len(table))
	for key := range table {
		list = append(list, key)
	}
	sort.Strings(list)
	return list
}

func control(w http.ResponseWriter, req *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(req.Body, 64<<10))
	args := leaves(body)
	key := args["NewProtocol"] + "/" + args["NewExternalPort"]
	fault := func(code int, text string) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code, text)
	}
	reply := func(inner string) {
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:R xmlns:u="%s">%s</u:R></s:Body></s:Envelope>`, service, inner)
	}
	describe := func(key string, f *forward) string {
		var text bytes.Buffer
		xml.EscapeText(&text, []byte(f.Description))
		parts := strings.SplitN(key, "/", 2)
		return "<NewRemoteHost></NewRemoteHost><NewExternalPort>" + parts[1] + "</NewExternalPort><NewProtocol>" + parts[0] + "</NewProtocol>" +
			"<NewInternalPort>" + f.Port + "</NewInternalPort><NewInternalClient>" + f.Client + "</NewInternalClient><NewEnabled>1</NewEnabled>" +
			"<NewPortMappingDescription>" + text.String() + "</NewPortMappingDescription><NewLeaseDuration>" + f.Lease + "</NewLeaseDuration>"
	}

	action := strings.Trim(req.Header.Get("SOAPAction"), `"`)
	action = action[strings.Index(action, "#")+1:]
	calls = append(calls, action+" "+key)
	if len(calls) > 2000 {
		calls = calls[len(calls)-2000:]
	}
	switch action {
	case "GetExternalIPAddress":
		reply("<NewExternalIPAddress>203.0.113.7</NewExternalIPAddress>")
	case "GetSpecificPortMappingEntry":
		if f, ok := table[key]; ok {
			reply(describe(key, f))
		} else {
			fault(714, "NoSuchEntryInArray")
		}
	case "GetGenericPortMappingEntry":
		index, err := strconv.Atoi(args["NewPortMappingIndex"])
		list := keys()
		if err != nil || index < 0 || index >= len(list) {
			fault(713, "SpecifiedArrayIndexInvalid")
			return
		}
		reply(describe(list[index], table[list[index]]))
	case "AddPortMapping":
		if code, ok := hidden[key]; ok {
			if code != 0 {
				fault(code, "ConflictInMappingEntry")
			} else {
				reply("")
			}
			return
		}
		if f, ok := table[key]; ok && f.Client != args["NewInternalClient"] {
			fault(718, "ConflictInMappingEntry")
			return
		}
		table[key] = &forward{Client: args["NewInternalClient"], Port: args["NewInternalPort"], Description: args["NewPortMappingDescription"], Lease: args["NewLeaseDuration"]}
		reply("")
	case "DeletePortMapping":
		if _, ok := table[key]; !ok {
			fault(714, "NoSuchEntryInArray")
			return
		}
		delete(table, key)
		reply("")
	default:
		fault(401, "Invalid Action")
	}
}

func main() {
	ip := ownAddress()
	location := "http://" + ip + ":5000/desc.xml"

	// SSDP: answer every search with the description's address
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 1900})
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			mu.Lock()
			silent := down
			mu.Unlock()
			if silent || !bytes.HasPrefix(buf[:n], []byte("M-SEARCH")) {
				continue
			}
			st := "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
			for _, line := range strings.Split(string(buf[:n]), "\r\n") {
				if strings.HasPrefix(strings.ToUpper(line), "ST:") {
					st = strings.TrimSpace(line[3:])
				}
			}
			conn.WriteToUDP([]byte("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nEXT:\r\nSERVER: fork-fake-igd UPnP/1.0\r\nST: "+st+"\r\nUSN: uuid:fork-fake-igd::"+st+"\r\nLOCATION: "+location+"\r\n\r\n"), from)
		}
	}()

	device := http.NewServeMux()
	device.HandleFunc("/desc.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
 <device>
  <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
  <friendlyName>Test Router (stand-in)</friendlyName>
  <deviceList><device><deviceList><device>
   <serviceList><service>
    <serviceType>%s</serviceType>
    <controlURL>/ctl</controlURL>
   </service></serviceList>
  </device></deviceList></device></deviceList>
 </device>
</root>`, service)
	})
	device.HandleFunc("/ctl", control)
	go func() { log.Fatal(http.ListenAndServe(":5000", device)) }()

	admin := http.NewServeMux()
	say := func(w http.ResponseWriter, value any) { json.NewEncoder(w).Encode(value) }
	admin.HandleFunc("/table", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		say(w, table)
	})
	admin.HandleFunc("/calls", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		say(w, calls)
	})
	admin.HandleFunc("/put", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		mu.Lock()
		defer mu.Unlock()
		table[q.Get("key")] = &forward{Client: q.Get("client"), Port: q.Get("port"), Description: q.Get("desc"), Lease: "0"}
		say(w, table)
	})
	admin.HandleFunc("/hide", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		code, _ := strconv.Atoi(q.Get("code"))
		mu.Lock()
		defer mu.Unlock()
		hidden[q.Get("key")] = code
		say(w, hidden)
	})
	admin.HandleFunc("/drop", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		delete(table, req.URL.Query().Get("key"))
		say(w, table)
	})
	admin.HandleFunc("/reboot", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		table = map[string]*forward{}
		say(w, table)
	})
	admin.HandleFunc("/down", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		down = req.URL.Query().Get("set") == "1"
		say(w, down)
	})
	log.Println("fake IGD at", location, "- control on :8080")
	log.Fatal(http.ListenAndServe(":8080", admin))
}
