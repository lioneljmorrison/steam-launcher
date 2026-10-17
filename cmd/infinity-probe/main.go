// Command infinity-probe prints what a Disney Infinity base reports as tags
// are placed and removed, to check which NFC cards it can read.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lioneljmorrison/steam-launcher/internal/infinity"
)

func main() {
	duration := flag.Duration("t", 2*time.Minute, "how long to listen")
	raw := flag.Bool("raw", true, "print every packet from the base")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	base, err := infinity.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer base.Close()
	if *raw {
		base.Raw = func(p []byte) { log.Printf("<- % x", p) }
	}
	if err := base.Activate(); err != nil {
		log.Fatalf("activate: %v", err)
	}
	log.Print("base activated; place and remove tags now")
	base.SetColor(infinity.PadAll, 0, 0, 40)

	deadline := time.After(*duration)
	for {
		select {
		case ev := <-base.Events:
			if !ev.Added {
				log.Printf("REMOVED pad=%d index=%d", ev.Pad, ev.Index)
				base.SetColor(ev.Pad, 0, 0, 0)
				continue
			}
			uid, err := base.TagUID(ev.Index)
			if err != nil {
				log.Printf("ADDED pad=%d index=%d uid error: %v", ev.Pad, ev.Index, err)
				base.SetColor(ev.Pad, 60, 0, 0)
				continue
			}
			log.Printf("ADDED pad=%d index=%d uid=%X", ev.Pad, ev.Index, uid)
			base.SetColor(ev.Pad, 0, 60, 0)
		case <-base.Done():
			log.Print("base disconnected")
			os.Exit(1)
		case <-deadline:
			base.SetColor(infinity.PadAll, 0, 0, 0)
			fmt.Println("done")
			return
		}
	}
}
