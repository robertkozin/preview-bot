package main

import (
	"crypto/sha1"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/bwmarrin/discordgo"
)

var DiscordToken = mustGetEnvString("DISCORD_TOKEN")
var PreviewDir = mustGetEnvString("PREVIEW_DIR")
var PreviewBaseUrl = mustGetEnvString("PREVIEW_BASE_URL")

// TODO: Improve this to include short links
var PreviewMatch = regexp.MustCompile(`\S+(?:tiktok\.com|instagram\.com|twitter\.com|reddit\.com)\S+`)

var BotId string

var SupressEmbeds = &struct {
	Flags int `json:"flags"`
}{
	Flags: 1 << 2,
}

func main() {
	os.MkdirAll(PreviewDir, os.ModePerm)
	// Start cleaning task
	go func() {
		for range time.Tick(1 * time.Hour) {
			if err := clean(previewDir, 10); err != nil {
				fmt.Println("err cleaning:", err)
			}
		}
	}()

	dg, _ := discordgo.New("Bot " + DiscordToken)

	dg.Identify.Intents = discordgo.IntentsGuildMessages
	dg.Identify.LargeThreshold = 50
	dg.Identify.GuildSubscriptions = false

	dg.SyncEvents = false
	dg.StateEnabled = false

	dg.AddHandler(ready)
	dg.AddHandler(messageCreate)

	err := dg.Open()
	if err != nil {
		fmt.Printf("err starting discordgo: %v\n", err)
		return
	}

	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	dg.Close()
}

func ready(s *discordgo.Session, m *discordgo.Ready) {
	BotId = m.User.ID
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == BotId {
		return
	}

	link := PreviewMatch.FindString(m.Content)
	if link == "" {
		return
	}

	// TODO: Support multiple links?
	// TODO: Process messages sync then handle links async

	output := preview(link)
	if output == "" {
		return
	}

	if _, err := s.ChannelMessageSend(m.ChannelID, PreviewBaseUrl+output); err != nil {
		fmt.Printf("err sending message: %v\n", err)
	}

	_, _ = s.RequestWithBucketID("PATCH", discordgo.EndpointChannelMessage(m.ChannelID, m.ID), SupressEmbeds, discordgo.EndpointChannelMessage(m.ChannelID, ""))

	// TODO: Clean output dir
}

func preview(url string) (path string) {
	hashUrl := fmt.Sprintf("%x", sha1.Sum([]byte(url)))[:7]

	outputFile := hashUrl + ".mp4"

	cmd := exec.Command(
		"yt-dlp",
		"--downloader", "ffmpeg", // Ffmpeg lets us limit video duration vs native downloader
		"--downloader-args", "ffmpeg:-to 60 -loglevel warning", // Limit to 60s
		"-S", "+vcodec:avc", // Prefer H264
		// Assume that the places we're downloading from already optimize for the web (faststart + H264)
		"--no-playlist",
		"-o", outputFile,
		"-P", PreviewDir,
		url,
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return ""
	}

	return outputFile
}

// If size of directory is greater than max then remove ~20% of the oldest files.
// Why leave 20% files? I don't know, 80/20 principal?
// Could be improved by using access time, but I'm not sure how I would get that information.
func clean(dir string, maxSizeGigabytes int) error {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return err
	}

	var maxSizeBytes int64 = int64(maxSizeGigabytes) * 1_000_000_000

	var totalDirSize int64
	for _, file := range files {
		totalDirSize += file.Size()
	}

	if totalDirSize <= maxSizeBytes {
		return nil
	}

	sort.Slice(files, func(i, j int) bool { // Sort most recent first
		return files[i].ModTime().After(files[j].ModTime())
	})

	var runningDirSize int64 = 0
	var targetDirSize int64 = int64(float64(maxSizeBytes) * 0.80)
	for _, file := range files {
		runningDirSize += file.Size()
		if runningDirSize > targetDirSize {
			if err = os.Remove(filepath.Join(dir, file.Name())); err != nil {
				return err
			}
		}
	}

	return nil
}

func mustGetEnvString(key string) (value string) {
	value, ok := os.LookupEnv(key)
	if !ok {
		panic("missing env var " + key)
	}

	return value
}
