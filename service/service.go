package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path"
	"time"

	"github.com/gen2brain/dlgs"
	"github.com/lucas-clemente/quic-go"
	"github.com/oleksandr/bonjour"
)

const (
	SERVER_HOST  = "0.0.0.0"
	SERVICE_PORT = ":9909"

	BONJOR_PORT      = 9908
	BONJOR_NAMESPACE = "FileShare"
	BONJOR_SERVICE   = "_fileshare._udp"
	BONJOR_DOMAIN    = "_local"

	MESSAGE_VERSION = 1
	TOPIC_STR       = "(%d,%d)"
)

var Instance Service

type StreamHandlerFunc func(r io.Reader, w io.Writer) (err error)
type Service struct {
	bonjonrs *bonjour.Server // 发现服务
	listener quic.Listener
	handlers map[string]StreamHandlerFunc
}

// 开启mdns和udp服务
func (svc *Service) Run() {
	if svc.bonjonrs != nil {
		dlgs.Warning("FileShare", "服务运行中")
		return
	}
	var err error
	svc.bonjonrs, err = bonjour.Register(BONJOR_NAMESPACE, BONJOR_SERVICE, BONJOR_DOMAIN, BONJOR_PORT, []string{SERVICE_PORT}, nil)
	if err != nil {
		log.Panicln(err.Error())
	}

	svc.listener, err = quic.ListenAddr(SERVER_HOST+SERVICE_PORT, GenerateTLSConfig(), nil)
	if err != nil {
		log.Panic(err)
	}
	svc.handlers = make(map[string]StreamHandlerFunc)
	svc.registerHandler(fmt.Sprintf(TOPIC_STR, 1, 1), svc.download)

	go func() {
		for {
			// 监听到新的连接，创建新的 goroutine 交给 handleConn函数 处理
			conn, err := svc.listener.Accept(context.Background())
			if err != nil {
				log.Println("conn err:", err)
			}
			go svc.handleConn(conn)
		}
	}()
}

func (svc *Service) Stop() {
	svc.bonjonrc.Exit <- true
	svc.bonjonrs.Shutdown()
	svc.listener.Close()
}

// mdns设备发现
func (svc *Service) discover() []*bonjour.ServiceEntry {
	bonjonrc, err := bonjour.NewResolver(nil)
	if err != nil {
		log.Panicln("Failed to initialize resolver:", err.Error())
	}
	var entries []*bonjour.ServiceEntry
	entriesc := make(chan *bonjour.ServiceEntry)
	go func(entriesc chan *bonjour.ServiceEntry) {
		for e := range entriesc {
			entries = append(entries, e)
			log.Println(e.HostName, e.AddrIPv4.String())
		}
	}(entriesc)
	err = bonjonrc.Lookup(BONJOR_NAMESPACE, BONJOR_SERVICE, BONJOR_DOMAIN, entriesc)
	if err != nil {
		log.Println("Failed to browse:", err.Error())
	}
	time.Sleep(time.Second * 1)
	bonjonrc.Exit <- true

	return entries
}

// quic连接处理
func (svc *Service) handleConn(conn quic.Connection) {
	log.Println("new connnect:", conn.RemoteAddr())

	for {
		stream, err := conn.AcceptStream(conn.Context())
		if err != nil {
			log.Println(err)
			return
		}

		go svc.handlerStream(stream)
	}
}

func (svc *Service) handlerStream(stream quic.Stream) {
	defer stream.Close()

	header := make([]byte, 3)
	length, err := stream.Read(header)
	if err != nil {
		log.Println(err)
		return
	}
	if length != 3 {
		log.Println("未知类型的数据流")
		return
	}
	if header[0] != MESSAGE_VERSION {
		log.Println("未知类型的数据流")
		return
	}

	topic := fmt.Sprintf(TOPIC_STR, header[1], header[2])
	shfunc, err := svc.findHandler(topic)
	if err != nil {
		log.Println(err)
		return
	}

	if err = shfunc(io.Reader(stream), io.Writer(stream)); err != nil {
		log.Println(err)
	}
}

func (svc *Service) findHandler(topic string) (StreamHandlerFunc, error) {
	if handler, ok := svc.handlers[topic]; ok {
		return handler, nil
	}
	return nil, fmt.Errorf("topic %s stream handler not found", topic)
}

func (svc *Service) registerHandler(topic string, shfunc StreamHandlerFunc) {
	if _, ok := svc.handlers[topic]; ok {
		log.Printf("topic %s stream handler overwritten")
	}
	svc.handlers[topic] = shfunc
}

func (svc *Service) download(r io.Reader, w io.Writer) (err error) {
	// 接收文件名大小
	tempbytes := make([]byte, 1)
	n, err := r.Read(tempbytes)
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("not received file size")
	}
	filenamelen := int(tempbytes[0])
	// 接收接收文件名
	tempbytes = make([]byte, filenamelen)
	n, err = r.Read(tempbytes)
	if err != nil {
		return err
	}
	if n != filenamelen {
		return errors.New("not received filename")
	}
	filename := string(tempbytes)
	// 接收文件大小
	tempbytes = make([]byte, 8)
	n, err = r.Read(tempbytes)
	if err != nil {
		return err
	}
	if n != 8 {
		return errors.New("not received file size")
	}
	filesize := binary.LittleEndian.Uint64(tempbytes)

	// 询问是否继续
	ok, _ := dlgs.Question(filename, fmt.Sprintf("想给您分享文件%s,%.2fMB。是否同意？", filename, float64(filesize)/1024/1024), false)
	if !ok {
		return
	}
	file, ok, _ := dlgs.File("选择保存位置", "选择文件夹", true)
	if !ok {
		return
	}
	file = fmt.Sprintf("%s/%s", file, filename)
	// 创建文件句柄
	fi, err := os.Create(file)
	if err != nil {
		log.Println(err)
		return
	}
	//函数结束关闭文件
	defer fi.Close()

	//定义写入流
	filebuf := bufio.NewWriter(fi)

	writen, err := io.Copy(filebuf, r)
	if err != nil {
		return fmt.Errorf("write file error: %v", err)
	}

	if filesize != uint64(writen) {
		return errors.New("data len != writen")
	}

	// err = os.Rename(file+".tmp", file)
	// if err != nil {
	// 	return fmt.Errorf("rename file error: %v", err)
	// }

	log.Println("文件传输成功")

	return
}

func (svc *Service) SendFile() {
	se := svc.discover()
	var hosts []string
	hostMap := make(map[string]*bonjour.ServiceEntry)
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
	filename := path.Base(filepath)

	log.Println(filepath, "->", h)
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"quic-echo-example"},
	}
	tohost := hostMap[h]
	conn, err := quic.DialAddr(tohost.AddrIPv4.String()+tohost.Text[0], tlsConf, nil)
	if err != nil {
		log.Panic(err)
	}
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		log.Panic(err)
	}

	// 写入头
	header := []byte{1, 1, 1}

	stream.Write(header)
	// 文件名大小
	tempbytes := make([]byte, 1)
	filenamelen := len(filename)
	tempbytes[0] = uint8(filenamelen)
	n, err := stream.Write(tempbytes)
	if err != nil {
		log.Println(err)
		return
	}
	if n != 1 {
		log.Println("filenamelen send faild")
		return
	}
	// 文件名
	n, err = stream.Write([]byte(filename))
	if err != nil {
		log.Println(err)
		return
	}
	if n != filenamelen {
		log.Println("filename send faild")
		return
	}

	fp, err := os.Open(filepath)
	if err != nil {
		log.Fatalf("open file error: %v\n", err)
	}
	defer fp.Close()
	fileInfo, err := fp.Stat()
	if err != nil {
		log.Fatalf("get file info error: %v\n", err)
	}
	filesize := uint64(fileInfo.Size())
	// 文件大小
	tempbytes = make([]byte, 8)
	binary.LittleEndian.PutUint64(tempbytes, filesize)
	n, err = stream.Write(tempbytes)
	if err != nil {
		log.Println(err)
		return
	}
	if n != 8 {
		log.Println("file size byte error")
		return
	}

	// tempbytes = make([]byte, 1)
	// n, err = stream.Read(tempbytes)
	// if err != nil {
	// 	log.Println(err)
	// 	return
	// }
	// if uint64(n) != 1 {
	// 	log.Println("confirm error")
	// 	return
	// }
	// if tempbytes[0] == 0 {
	// 	dlgs.Info("FileShare", "对方不同意此次传输")
	// 	return
	// }

	wn, err := io.Copy(stream, fp)
	if err != nil {
		log.Println(err)
		return
	}
	if uint64(wn) != filesize {
		log.Println("write file n != filesize")
		return
	}

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
