package main

import (
    "context"
    "fmt"
    "time"

    "github.com/nodeox/NodePro/internal/transport"
    "github.com/quic-go/quic-go"
)

func main() {
    cm, err := transport.NewCertManager("configs/certs/server.crt", "configs/certs/server.key", "configs/certs/ca.crt", true)
    if err != nil { panic(err) }

    tlsCfg := cm.GetTLSConfigServer()
    ln, err := quic.ListenAddr("127.0.0.1:15443", tlsCfg, &quic.Config{EnableDatagrams:true})
    if err != nil {
        fmt.Println("listen err:", err)
        return
    }
    defer ln.Close()
    fmt.Println("listening")

    go func(){
        c, err := ln.Accept(context.Background())
        fmt.Println("accept:", err)
        if err == nil { c.CloseWithError(0, "") }
    }()

    d, err := transport.NewQUICDialer("configs/certs/client.crt", "configs/certs/client.key", "configs/certs/ca.crt", "localhost", true)
    if err != nil { panic(err) }
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    conn, err := d.Dial(ctx, "127.0.0.1:15443")
    fmt.Printf("dial result err=%v conn_nil=%v\n", err, conn==nil)
}
