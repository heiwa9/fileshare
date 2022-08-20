package main

import (
	_ "embed"
	"fileshare/service"
	"log"

	"github.com/getlantern/systray"
)

//go:embed icon/Archive.ico
var ricon []byte

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(ricon)
	// systray.SetTitle("fileshare")
	systray.SetTooltip("fileshare")

	mOpen := systray.AddMenuItem("开启服务", "run mdns and udp server")
	mSele := systray.AddMenuItem("发送文件", "select host")
	mQuit := systray.AddMenuItem("退出", "quit")

	go func() {
		for {
			select {
			case <-mQuit.ClickedCh:
				log.Println("app quit")
				systray.Quit()
				return
			case <-mOpen.ClickedCh:
				log.Println("run mdns & udp")
				service.Instance.Run()
			case <-mSele.ClickedCh:
				service.Instance.SendFile()
			}
		}
	}()
}

func onExit() {
	service.Instance.Stop()
}
