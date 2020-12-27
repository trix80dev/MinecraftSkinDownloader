package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/pelletier/go-toml"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
	"image"
	"image/color"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

type minecraftGeometry struct {
	Description struct {
		Identifier string `json:"identifier"`
	}
}

const DefaultSkinResourcePatch2 = `{"geometry":{"default":"geometry.hive.costume.thecrow"},"animations":{}}`

var myConn *minecraft.Conn
var skin protocol.Skin

// The following program implements a proxy that forwards players from one local address to a remote address.
func main() {
	config := readConfig()
	src := TokenSource2(config.Connection.Token)

	listener, err := minecraft.Listen("raknet", config.Connection.LocalAddress)
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	go doListener()
	for {
		c, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		go handleConn(c.(*minecraft.Conn), listener, config, src)
	}
}

// handleConn handles a new incoming minecraft.Conn from the minecraft.Listener passed.
func handleConn(conn *minecraft.Conn, listener *minecraft.Listener, config config, src oauth2.TokenSource) {
	serverConn, err := minecraft.Dialer{
		TokenSource: src,
		ClientData:  conn.ClientData(),
	}.Dial("raknet", config.Connection.RemoteAddress)
	if err != nil {
		panic(err)
	}
	var g sync.WaitGroup
	g.Add(2)
	go func() {
		if err := conn.StartGame(serverConn.GameData()); err != nil {
			panic(err)
		}
		g.Done()
	}()
	go func() {
		if err := serverConn.DoSpawn(); err != nil {
			panic(err)
		}
		g.Done()
	}()
	g.Wait()

	myConn = serverConn

	go func() {
		defer listener.Disconnect(conn, "connection lost")
		defer serverConn.Close()
		for {
			pk, err := conn.ReadPacket()
			if err != nil {
				return
			}
			if err := serverConn.WritePacket(pk); err != nil {
				return
			}
		}
	}()
	go func() {
		defer serverConn.Close()
		defer listener.Disconnect(conn, "connection lost")
		for {
			pk, err := serverConn.ReadPacket()
			if err != nil {
				return
			}

			switch p := pk.(type) {
			case *packet.PlayerSkin:
				//skinToFile(p.Skin)
				unique, _ := uuid.Parse(myConn.IdentityData().Identity)
				if p.UUID == unique {
					skin = p.Skin
					fmt.Printf("Persona?: %v\tPremium: %v\tID: %v\tTrusted: %v\tColor: %v\tResourcePatch: %v\n", p.Skin.PersonaSkin, p.Skin.PremiumSkin, p.Skin.SkinID, p.Skin.Trusted, p.Skin.SkinColour, string(p.Skin.SkinResourcePatch))
				}

			case *packet.PlayerList:
				for _, v := range p.Entries {
					skinToFile(v.Skin)
				}
			}

			if err := conn.WritePacket(pk); err != nil {
				return
			}
		}
	}()
}

func doListener(){
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := strings.Split(scanner.Text(), " ")
		switch input[0] {
		case "skin":
			//skin := fileToSkin(input[1])
			unique, _ := uuid.Parse(myConn.IdentityData().Identity)
			p := &packet.PlayerSkin{
				Skin: skin,
				UUID: unique,
			}
			if err := myConn.WritePacket(p); err != nil {
				fmt.Println(err)
			}
		}
	}
}

func fileToSkin(file string) protocol.Skin {

	path := strings.ReplaceAll(file, ".", "/")
	skinfile, err := os.Open("skin/" + path + "/skin.png")
	if err != nil {
		fmt.Println(err)
	}
	img, err := png.Decode(skinfile)
	if err != nil {
		fmt.Println(err)
	}
	var pix []byte
	for y:= 0; y < img.Bounds().Size().Y; y++ {
		for x := 0; x < img.Bounds().Size().X; x++{
			c := img.At(x, y)
			r, g, b, a := c.RGBA()
			pix = append(pix, byte(r), byte(g), byte(b), byte(a))
		}
	}

	geoFile, err := os.Open("skin/" + path + "/geometry.json")
	if err != nil {
		fmt.Println(err)
	}

	geo, err := ioutil.ReadAll(geoFile)
	if err != nil {
		fmt.Println(err)
	}

	if err := geoFile.Close(); err != nil {
		fmt.Println(err)
	}

	return protocol.Skin{
		SkinID: myConn.ClientData().SkinID,
		SkinData:	pix,
		SkinGeometry: geo,
		SkinResourcePatch: []byte(DefaultSkinResourcePatch2),
		SkinImageWidth: uint32(img.Bounds().Size().X),
		SkinImageHeight: uint32(img.Bounds().Size().Y),
		Animations: make([]protocol.SkinAnimation, 0),
		PersonaPieces: make([]protocol.PersonaPiece, 0),
		PieceTintColours: make([]protocol.PersonaPieceTintColour, 0),
		Trusted: true,
		ArmSize: "wide",
		SkinColour: "",
		PremiumSkin: false,
		PersonaSkin: false,

	}

}

func skinToFile(skin protocol.Skin){
	img := image.NewRGBA(image.Rect(0, 0, int(skin.SkinImageWidth), int(skin.SkinImageHeight)))
	for y := 0; y < int(skin.SkinImageHeight); y ++ {
		for x:= 0; x < int(skin.SkinImageWidth); x++ {
			index := y * int(skin.SkinImageWidth) * 4 + x * 4
			c := color.RGBA{
				R: skin.SkinData[index],
				G: skin.SkinData[index + 1],
				B: skin.SkinData[index + 2],
				A: skin.SkinData[index + 3],
			}
			img.SetRGBA(x, y, c)
		}
	}

	data := struct {
		MinecraftGeometry []minecraftGeometry `json:"minecraft:geometry"`
	}{}
	err := json.Unmarshal(skin.SkinGeometry, &data); if err != nil {
		//err
		return
	}
	if len(data.MinecraftGeometry) == 0 {
		return
	}
		s := data.MinecraftGeometry[0].Description.Identifier
	s = strings.ReplaceAll(s, ".", "/")

	if strings.Contains(s, "persona") {
		return
	}

	if os.Stat("skin/" + s); os.IsExist(err){
		return
	}
	fmt.Println(s)
	os.MkdirAll("skin/" + s, os.ModePerm)

	_ = ioutil.WriteFile("skin/" + s + "/geometry.json", skin.SkinGeometry, os.ModePerm)

	f, err := os.Create("skin/" + s + "/skin.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	Connection struct {
		LocalAddress  string
		RemoteAddress string
		Token string
	}
}

func readConfig() config {
	c := config{}
	if _, err := os.Stat("config.toml"); os.IsNotExist(err) {
		f, err := os.Create("config.toml")
		if err != nil {
			log.Fatalf("error creating config: %v", err)
		}
		data, err := toml.Marshal(c)
		if err != nil {
			log.Fatalf("error encoding default config: %v", err)
		}
		if _, err := f.Write(data); err != nil {
			log.Fatalf("error writing encoded default config: %v", err)
		}
		_ = f.Close()
	}
	data, err := ioutil.ReadFile("config.toml")
	if err != nil {
		log.Fatalf("error reading config: %v", err)
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		log.Fatalf("error decoding config: %v", err)
	}
	if c.Connection.LocalAddress == "" {
		c.Connection.LocalAddress = "0.0.0.0:19132"
	}
	data, _ = toml.Marshal(c)
	if err := ioutil.WriteFile("config.toml", data, 0644); err != nil {
		log.Fatalf("error writing config file: %v", err)
	}
	return c
}

func TokenSource2(identifier string) oauth2.TokenSource {
	check := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	token := new(oauth2.Token)
	tokenData, err := ioutil.ReadFile("token/" + identifier + "token.tok")
	if err == nil {
		_ = json.Unmarshal(tokenData, token)
	} else {
		token, err = auth.RequestLiveToken()
		check(err)
	}
	src := auth.RefreshTokenSource(token)
	_, err = src.Token()
	if err != nil {
		// The cached refresh token expired and can no longer be used to obtain a new token. We require the
		// user to log in again and use that token instead.
		token, err = auth.RequestLiveToken()
		check(err)
		src = auth.RefreshTokenSource(token)
	}
	go func() {
		c := make(chan os.Signal, 3)
		signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
		<-c

		tok, _ := src.Token()
		b, _ := json.Marshal(tok)
		_ = ioutil.WriteFile("token/" + identifier + "token.tok", b, 0644)
		os.Exit(0)
	}()
	return src
}
