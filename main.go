package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type Song struct {
	Name          string
	SongLink      string
	LengthSeconds int
	DownloadLinks map[string]string // format -> URL
	Sizes         map[string]int    // format -> size in KB
}

type Album struct {
	Name        string
	AlbumLink   string
	AlbumImages []string
	Songs       []*Song
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: khinsider_downloader <album_url> [options]")
		fmt.Println("\nOptions:")
		fmt.Println("  --format mp3|flac    Download format (default: flac)")
		fmt.Println("  --no-images          Skip downloading album images")
		return
	}

	albumURL := os.Args[1]
	downloadFormat := "flac"
	downloadImages := true

	// Parse command line arguments
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--format":
			if i+1 < len(os.Args) {
				downloadFormat = strings.ToLower(os.Args[i+1])
				i++
			}
		case "--no-images":
			downloadImages = false
		}
	}

	// Parse the album page
	album, err := ParseAlbumPage(albumURL)
	if err != nil {
		fmt.Printf("Error parsing album: %v\n", err)
		return
	}

	fmt.Printf("Album: %s\n", album.Name)
	fmt.Printf("Songs: %d\n", len(album.Songs))
	fmt.Printf("Download format: %s\n", strings.ToUpper(downloadFormat))

	// Create download directory
	sanitizedName := sanitizeFilename(album.Name)
	downloadDir := filepath.Join("downloads", sanitizedName)
	os.MkdirAll(downloadDir, 0755)

	// Download songs
	fmt.Println("\nDownloading songs...")
	successCount := 0
	failCount := 0

	for i, song := range album.Songs {
		fmt.Printf("[%d/%d] %s\n", i+1, len(album.Songs), song.Name)

		// Get download links for this song
		err := ParseDownloadLinks(song)
		if err != nil {
			fmt.Printf("  Error getting download links: %v\n", err)
			failCount++
			continue
		}

		// Select download URL based on format preference
		var downloadURL string

		formatUpper := strings.ToUpper(downloadFormat)
		if url, ok := song.DownloadLinks[formatUpper]; ok {
			downloadURL = url
		} else if formatUpper == "FLAC" {
			// Fallback to MP3 if FLAC not available
			if url, ok := song.DownloadLinks["MP3"]; ok {
				downloadURL = url
				fmt.Printf("  FLAC not available, using MP3\n")
			}
		} else {
			// Get first available format
			for _, url := range song.DownloadLinks {
				downloadURL = url
				break
			}
		}

		if downloadURL == "" {
			fmt.Printf("  No download link found\n")
			failCount++
			continue
		}

		// Extract original filename from URL
		parsedURL, err := url.Parse(downloadURL)
		if err != nil {
			fmt.Printf("  Error parsing download URL: %v\n", err)
			failCount++
			continue
		}

		// Get the original filename from the URL
		originalFilename := filepath.Base(parsedURL.Path)
		if originalFilename == "" || originalFilename == "/" {
			// Fallback to generated name if we can't get original
			ext := filepath.Ext(downloadURL)
			if ext == "" {
				ext = "." + strings.ToLower(formatUpper)
			}
			originalFilename = fmt.Sprintf("%03d - %s%s", i+1, sanitizeFilename(song.Name), ext)
		}

		filePath := filepath.Join(downloadDir, originalFilename)

		err = downloadFile(downloadURL, filePath)
		if err != nil {
			fmt.Printf("  Error downloading: %v\n", err)
			failCount++
			continue
		}

		fmt.Printf("  Downloaded: %s\n", originalFilename)
		successCount++

		// Be nice to the server
		time.Sleep(500 * time.Millisecond)
	}

	// Download album images
	if downloadImages && len(album.AlbumImages) > 0 {
		fmt.Println("\nDownloading album images...")
		imageDir := filepath.Join(downloadDir, "Art")
		os.MkdirAll(imageDir, 0755)

		for i, imgURL := range album.AlbumImages {
			if !strings.HasPrefix(imgURL, "http") {
				imgURL = "https://downloads.khinsider.com" + imgURL
			}

			// Extract original filename from URL
			parsedURL, err := url.Parse(imgURL)
			if err != nil {
				fmt.Printf("Error parsing image URL %s: %v\n", imgURL, err)
				continue
			}

			// Get the last part of the path as filename
			originalFilename := filepath.Base(parsedURL.Path)
			if originalFilename == "" || originalFilename == "/" {
				originalFilename = fmt.Sprintf("cover_%d.jpg", i)
			}

			err = downloadFile(imgURL, filepath.Join(imageDir, originalFilename))
			if err != nil {
				fmt.Printf("Error downloading image %s: %v\n", imgURL, err)
			} else {
				fmt.Printf("Downloaded: %s\n", originalFilename)
			}
		}
	}

	fmt.Printf("\n=== Download Summary ===\n")
	fmt.Printf("Successful: %d\n", successCount)
	fmt.Printf("Failed: %d\n", failCount)
	fmt.Printf("Files saved to: %s\n", downloadDir)
}

func ParseAlbumPage(albumURL string) (*Album, error) {
	doc, err := fetchHTML(albumURL)
	if err != nil {
		return nil, err
	}

	album := &Album{
		AlbumLink:   albumURL,
		AlbumImages: make([]string, 0),
		Songs:       make([]*Song, 0),
	}

	// Get album name
	doc.Find("#pageContent h2").First().Each(func(i int, s *goquery.Selection) {
		album.Name = strings.TrimSpace(s.Text())
	})

	// Get album images
	doc.Find("div.albumImage a").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			album.AlbumImages = append(album.AlbumImages, href)
		}
	})

	// Parse song list
	songTable := doc.Find("table#songlist")
	if songTable.Length() == 0 {
		return album, nil
	}

	// Parse songs
	songTable.Find("tbody tr").Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		if strings.Contains(id, "songlist_footer") {
			return
		}

		song := &Song{
			DownloadLinks: make(map[string]string),
			Sizes:         make(map[string]int),
		}

		// Get song name and link
		s.Find("td.clickable-row a").First().Each(func(j int, a *goquery.Selection) {
			song.Name = strings.TrimSpace(a.Text())
			if href, exists := a.Attr("href"); exists {
				song.SongLink = "https://downloads.khinsider.com" + href
			}
		})

		// Get duration
		s.Find("td.clickable-row").Eq(1).Each(func(j int, td *goquery.Selection) {
			duration := strings.TrimSpace(td.Text())
			song.LengthSeconds = convertToSeconds(duration)
		})

		if song.Name != "" {
			album.Songs = append(album.Songs, song)
		}
	})

	return album, nil
}

func ParseDownloadLinks(song *Song) error {
	if song.SongLink == "" {
		return fmt.Errorf("no song link available")
	}

	doc, err := fetchHTML(song.SongLink)
	if err != nil {
		return err
	}

	// Find download links
	doc.Find("#pageContent p a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists || !strings.HasPrefix(href, "https://") {
			return
		}

		// Extract format from URL
		ext := strings.ToUpper(filepath.Ext(href))
		if len(ext) > 1 {
			ext = ext[1:] // Remove the dot
			song.DownloadLinks[ext] = href
		}
	})

	return nil
}

func fetchHTML(url string) (*goquery.Document, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

func downloadFile(fileURL, filepath string) error {
	// Parse URL to handle relative paths
	parsedURL, err := url.Parse(fileURL)
	if err != nil {
		return err
	}

	if parsedURL.Scheme == "" {
		fileURL = "https://downloads.khinsider.com" + fileURL
	}

	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://downloads.khinsider.com/")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("status code: %d", resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func convertToSeconds(duration string) int {
	parts := strings.Split(duration, ":")
	if len(parts) != 2 {
		return 0
	}

	minutes, _ := strconv.Atoi(parts[0])
	seconds, _ := strconv.Atoi(parts[1])

	return minutes*60 + seconds
}

func sanitizeFilename(name string) string {
	// Remove invalid characters
	reg := regexp.MustCompile(`[<>:"/\\|?*]`)
	name = reg.ReplaceAllString(name, "")

	// Replace spaces with underscores
	//name = strings.ReplaceAll(name, " ", "_")

	// Limit length
	if len(name) > 200 {
		name = name[:200]
	}

	return name
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
