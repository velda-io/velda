// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package agentd

import (
	"context"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"velda.io/velda/pkg/proto"
)

const dnsDomainSuffix = ".local.velda."

type DnsServer struct {
	dnsServer    *dns.Server
	brokerClient proto.BrokerServiceClient
	ctx          atomic.Value
}

func NewDnsServer(addr, net string, brokerClient proto.BrokerServiceClient) *DnsServer {
	dnsServer := &dns.Server{
		Addr: addr,
		Net:  net,
	}
	server := &DnsServer{
		dnsServer:    dnsServer,
		brokerClient: brokerClient,
	}
	server.Reset()

	dns.HandleFunc(dnsDomainSuffix[1:], server.handleDNSRequest)
	dns.HandleFunc(".", server.forwardQuery)
	return server
}

func (s *DnsServer) SetContext(ctx context.Context) {
	s.ctx.Store(&ctx)
}

func (s *DnsServer) Reset() {
	var ctx context.Context
	s.ctx.Store(&ctx)
}

// handleDNSRequest handles incoming DNS queries
func (s *DnsServer) handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	msg := dns.Msg{}
	msg.SetReply(r)
	msg.Authoritative = true

	handled := false
	ctx := s.ctx.Load().(*context.Context)

	// Handle specific queries locally
	for _, q := range r.Question {
		log.Printf("Received query for domain: %s\n", q.Name)

		if q.Qtype == dns.TypeA && strings.HasSuffix(q.Name, dnsDomainSuffix) {
			// No session active.
			if ctx == nil {
				dns.HandleFailed(w, r)
				return
			}
			instanceId := (*ctx).Value("instanceId").(int64)
			svcName := strings.TrimSuffix(q.Name, dnsDomainSuffix)
			// TODO: Cache this.
			sessionRes, err := s.brokerClient.ListSessions(*ctx, &proto.ListSessionsRequest{
				InstanceId:  instanceId,
				ServiceName: svcName,
			})
			if err != nil {
				log.Printf("Error fetching sessions for service %s: %v", svcName, err)
				dns.HandleFailed(w, r)
				return
			}
			sessions := sessionRes.Sessions
			if len(sessions) > 1 {
				// Randomlize the first session.
				inx := rand.Intn(len(sessions))
				tmp := sessions[0]
				sessions[0] = sessions[inx]
				sessions[inx] = tmp
			}
			for _, session := range sessions {
				rr, err := dns.NewRR(q.Name + " 300 IN A " + session.InternalIpAddress)
				if err != nil {
					log.Printf("Error creating DNS record: %v\n", err)
					continue
				}
				msg.Answer = append(msg.Answer, rr)
				handled = true
			}
		}
	}
	if !handled {
		dns.HandleFailed(w, r)
	} else {
		if err := w.WriteMsg(&msg); err != nil {
			log.Printf("Failed to write DNS response: %v", err)
		}
	}
}
func (s *DnsServer) forwardQuery(w dns.ResponseWriter, r *dns.Msg) {
	// Use the default resolver to resolve the query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, q := range r.Question {
		hostname := q.Name

		// Resolve different types of records
		switch q.Qtype {
		case dns.TypeA:
			addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", hostname)
			if err != nil {
				log.Printf("Error resolving domain %s: %v", hostname, err)
				continue
			}

			// Construct DNS answer
			msg := dns.Msg{}
			msg.SetReply(r)
			for _, addr := range addrs {
				rr, err := dns.NewRR(hostname + " 300 IN A " + addr.String())
				if err != nil {
					log.Printf("Error creating DNS record: %v\n", err)
					continue
				}
				msg.Answer = append(msg.Answer, rr)
			}

			// Write the response
			if len(msg.Answer) > 0 {
				if err := w.WriteMsg(&msg); err != nil {
					log.Printf("Failed to write DNS response: %v", err)
				}
				return
			}
		case dns.TypeAAAA:
			addrs, err := net.DefaultResolver.LookupIP(ctx, "ip6", hostname)
			if err != nil {
				log.Printf("Error resolving domain %s: %v", hostname, err)
				continue
			}

			// Construct DNS answer
			msg := dns.Msg{}
			msg.SetReply(r)
			for _, addr := range addrs {
				rr, err := dns.NewRR(hostname + " 300 IN AAAA " + addr.String())
				if err != nil {
					log.Printf("Error creating DNS record: %v\n", err)
					continue
				}
				msg.Answer = append(msg.Answer, rr)
			}

			// Write the response
			if len(msg.Answer) > 0 {
				if err := w.WriteMsg(&msg); err != nil {
					log.Printf("Failed to write DNS response: %v", err)
				}
				return
			}
		default:
			log.Printf("Unsupported query type: %d\n", q.Qtype)
		}
	}

	// If no response could be constructed, return SERVFAIL
	dns.HandleFailed(w, r)
}

func (s *DnsServer) Run() error {
	log.Println("Starting DNS server")
	err := s.dnsServer.ListenAndServe()
	if err != nil {
		log.Printf("Failed to start DNS server: %v", err)
	}
	return err
}

func (s *DnsServer) Shutdown(ctx context.Context) error {
	return s.dnsServer.ShutdownContext(ctx)
}
