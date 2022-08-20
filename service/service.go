package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fileshare/model"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/gen2brain/dlgs"
	"github.com/grandcat/zeroconf"
	"github.com/lucas-clemente/quic-go"
)

const (
	SERVER_HOST  = "0.0.0.0"
	SERVICE_PORT = ":9909"
	MDNS_PORT    = 9908
)

var Instance Service

type Service struct {
	m *zeroconf.Server // 发现服务
}

// 开启mdns和udp服务
func (svc *Service) Run() {
	var err error
	svc.m, err = zeroconf.Register("fileshare", "_workstation._udp", "local.", MDNS_PORT, []string{SERVICE_PORT}, nil)
	if err != nil {
		log.Panicln(err)
	}

	listener, err := quic.ListenAddr(SERVER_HOST+SERVICE_PORT, GenerateTLSConfig(), nil)
	if err != nil {
		log.Panic(err)
	}

	go func() {
		for {
			// 监听到新的连接，创建新的 goroutine 交给 handleConn函数 处理
			conn, err := listener.Accept(context.Background())
			if err != nil {
				log.Println("conn err:", err)
			}
			go svc.handleConn(conn)
		}
	}()
}

func (svc *Service) Stop() {
	svc.m.Shutdown()
}

func (svc *Service) handleConn(conn quic.Connection) {
	log.Println("new connnect:", conn.RemoteAddr())

	stream, err := conn.AcceptStream(conn.Context())
	if err != nil {
		log.Println(err)
		return
	}
	scanner := bufio.NewScanner(stream)
	scanner.Split(PackSlitFunc)
	for scanner.Scan() {
		msg, err := Unpack(scanner.Bytes())
		if err != nil {
			log.Println(err)
			continue
		}

		//接收文件
		if msg.TopicIs(1, 1) {
			var f model.File
			_, err = f.UnmarshalMsg(msg.Body)
			if err != nil {
				log.Panicln(err)
				break
			}
			ok, _ := dlgs.Question(f.HostName, conn.RemoteAddr().String()+"想给您分享"+f.FileName+"\n是否同意?", false)
			if !ok {
				break
			}
			s, ok, _ := dlgs.File("选择保存位置", "选择文件夹", true)
			if !ok {
				break
			}
			err = os.WriteFile(s+"/"+f.FileName, f.Content, 0555)
			if err != nil {
				log.Println(err)
			}
			break
		}
	}
}

func (svc *Service) discover() []*zeroconf.ServiceEntry {
	// Discover all services on the network (e.g. _workstation._tcp)
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

	var entrys []*zeroconf.ServiceEntry
	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			entrys = append(entrys, entry)
		}
		log.Println("No more entries.")
	}(entries)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*200)
	defer cancel()
	err = resolver.Browse(ctx, "_workstation._udp", "local.", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}
	<-ctx.Done()

	return entrys
}

func (svc *Service) SendFile() {
	se := svc.discover()
	var hosts []string
	hostMap := make(map[string]*zeroconf.ServiceEntry)
	for _, v := range se {
		hostMap[v.HostName] = v
		hosts = append(hosts, v.HostName)
	}
	h, ok, _ := dlgs.List("发送文件", "请选择接收的主机", hosts)
	if !ok {
		return
	}
	filepath, ok, _ := dlgs.File("文件", "", false)
	if !ok {
		return
	}
	fileb, err := os.ReadFile(filepath)
	if err != nil {
		log.Println(err)
		return
	}

	s := strings.Split(filepath, "/")
	hname, _ := os.Hostname()
	mfbs, _ := (&model.File{
		HostName: hname,
		FileName: s[len(s)-1],
		Content:  fileb,
	}).MarshalMsg(nil)

	log.Println(filepath, "->", h)

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}

	tohost := hostMap[h]
	conn, err := quic.DialAddr(tohost.AddrIPv4[0].String()+tohost.Text[0], tlsConf, nil)
	if err != nil {
		log.Panic(err)
	}
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		log.Panic(err)
	}
	stream.Write(Pack(1, 1, mfbs))
}

// Setup a bare-bones TLS config for the server
func GenerateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"quic-echo-example"},
	}
}
