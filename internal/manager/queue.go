package manager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"goyt/internal/config"
	"goyt/internal/core"
	"goyt/internal/utils"
)

// Sentinel errors returned by AddDownload so the API layer can map them to
// user-facing messages without string matching.
var (
	ErrPlaylistURL       = errors.New("playlist URL detected - use playlist-specific endpoints instead")
	ErrAlreadyProcessing = errors.New("this URL is already being processed")
	ErrAlreadyDownloaded = errors.New("this URL has already been downloaded with the same quality and format")
	ErrActiveDownload    = errors.New("this URL is already being downloaded with the same quality and format")
	ErrPreviouslyFailed  = errors.New("this URL was previously attempted with the same settings. Remove the failed download first to retry")
	ErrQueueFull         = errors.New("download queue is full")
)

type DownloadManager struct {
	downloader       *core.Downloader
	downloads        map[string]*core.Download
	queue            chan *core.Download
	maxConcurrent    int
	activeWorkers    int             // Track active workers
	workerCtx        context.Context // Separate context for workers
	workerCancel     context.CancelFunc
	workerQuit       chan struct{} // Signals a single worker to stop after its current job
	progressChannels map[string]chan core.DownloadProgress
	cancelFuncs      map[string]context.CancelFunc
	pausedDownloads  map[string]*core.Download
	processingUrls   map[string]bool // Track URLs currently being processed
	convertSem       chan struct{}   // bounds concurrent convert jobs
	mutex            sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	outputDir        string
	config           *config.Config
}

func NewDownloadManager(
	downloader *core.Downloader,
	maxConcurrent int,
	outputDir string,
	cfg *config.Config,
) *DownloadManager {
	ctx, cancel := context.WithCancel(context.Background())
	workerCtx, workerCancel := context.WithCancel(ctx)

	dm := &DownloadManager{
		downloader:       downloader,
		downloads:        make(map[string]*core.Download),
		queue:            make(chan *core.Download, 100),
		maxConcurrent:    maxConcurrent,
		activeWorkers:    0,
		workerCtx:        workerCtx,
		workerCancel:     workerCancel,
		workerQuit:       make(chan struct{}),
		progressChannels: make(map[string]chan core.DownloadProgress),
		cancelFuncs:      make(map[string]context.CancelFunc),
		pausedDownloads:  make(map[string]*core.Download),
		processingUrls:   make(map[string]bool),
		convertSem:       make(chan struct{}, maxConcurrent),
		ctx:              ctx,
		cancel:           cancel,
		outputDir:        outputDir,
		config:           cfg,
	}

	// Start workers
	dm.startWorkers(maxConcurrent)

	if err := dm.LoadState(); err != nil {
		log.Printf("[MANAGER] Failed to load previous state: %v", err)
	}

	// Start cleanup worker if auto-expiry is enabled
	if cfg.CompletedFileExpiryHours > 0 {
		go dm.cleanupWorker()
	}

	// Start periodic state saving
	dm.StartPeriodicStateSave()

	return dm
}

func (dm *DownloadManager) AddDownload(req core.DownloadRequest) (*core.Download, error) {
	// Check if URL is a playlist - don't auto-process playlists
	if dm.downloader.IsPlaylistURL(req.URL) {
		return nil, ErrPlaylistURL
	}

	// Check if this URL is already being processed
	dm.mutex.Lock()
	if dm.processingUrls[req.URL] {
		dm.mutex.Unlock()
		return nil, ErrAlreadyProcessing
	}

	// Check if this URL with same type/quality/format is already present
	for _, download := range dm.downloads {
		if download.URL == req.URL &&
			download.Type == req.Type &&
			download.Quality == req.Quality &&
			download.Format == req.Format {

			// Provide specific error message based on status
			switch download.Status {
			case core.StatusQueued, core.StatusDownloading, core.StatusPostProcessing:
				dm.mutex.Unlock()
				return nil, ErrActiveDownload
			case core.StatusCompleted, core.StatusAlreadyExists:
				dm.mutex.Unlock()
				return nil, ErrAlreadyDownloaded
			case core.StatusFailed:
				dm.mutex.Unlock()
				return nil, ErrPreviouslyFailed
			}
		}
	}

	// Mark URL as being processed
	dm.processingUrls[req.URL] = true
	dm.mutex.Unlock()

	download := &core.Download{
		ID:        core.GenerateID(),
		URL:       req.URL,
		Type:      req.Type,
		Quality:   req.Quality,
		Format:    req.Format,
		Status:    core.StatusQueued,
		CreatedAt: time.Now(),
	}

	if utils.VerboseLogging {
		utils.LogDebugf("[MANAGER] Adding download %s to queue: URL=%s, Type=%s", download.ID, req.URL, req.Type)
	}

	// File already on disk (e.g. the ledger was cleared but the file remains):
	// report it as already downloaded so the UI shows a toast instead of adding
	// a duplicate ledger entry.
	if existingFile := dm.checkFileExists(req); existingFile != "" {
		log.Printf("[MANAGER] Download %s: File already exists at %s, not queuing", download.ID, existingFile)
		dm.mutex.Lock()
		delete(dm.processingUrls, req.URL)
		dm.mutex.Unlock()
		return nil, ErrAlreadyDownloaded
	}

	dm.mutex.Lock()
	dm.downloads[download.ID] = download
	dm.progressChannels[download.ID] = make(chan core.DownloadProgress, 10)
	dm.mutex.Unlock()

	// Add to queue
	select {
	case dm.queue <- download:
		if utils.VerboseLogging {
			utils.LogDebugf("[MANAGER] Download %s added to queue successfully", download.ID)
		}
		return download, nil
	default:
		log.Printf("[MANAGER] Download queue is full, rejecting download %s", download.ID)
		dm.mutex.Lock()
		delete(dm.downloads, download.ID)
		delete(dm.progressChannels, download.ID)
		delete(dm.processingUrls, req.URL)
		dm.mutex.Unlock()
		return nil, ErrQueueFull
	}
}

func (dm *DownloadManager) AddPlaylistDownload(req core.DownloadRequest) (*core.Download, error) {
	log.Printf("[MANAGER] Processing playlist URL: %s", req.URL)

	// Get playlist items
	items, err := dm.downloader.GetPlaylistItems(req.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist items: %w", err)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no items found in playlist")
	}

	log.Printf("[MANAGER] Found %d items in playlist, creating individual downloads", len(items))

	// Create a download for each playlist item
	var firstDownload *core.Download
	for i, item := range items {
		itemURL := item.URL
		if itemURL == "" {
			// flat-playlist entries from YouTube may omit the URL field
			itemURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.ID)
		}
		download := &core.Download{
			ID:        core.GenerateID(),
			URL:       itemURL,
			Type:      req.Type,
			Quality:   req.Quality,
			Format:    req.Format,
			Status:    core.StatusQueued,
			Title:     item.Title,
			CreatedAt: time.Now(),
		}

		dm.mutex.Lock()
		dm.downloads[download.ID] = download
		dm.progressChannels[download.ID] = make(chan core.DownloadProgress, 10)
		dm.mutex.Unlock()

		// Add to queue
		select {
		case dm.queue <- download:
			log.Printf("[MANAGER] Playlist item %d/%d added to queue: %s", i+1, len(items), item.Title)
			if firstDownload == nil {
				firstDownload = download
			}
		default:
			log.Printf("[MANAGER] Download queue is full, skipping remaining %d playlist items", len(items)-i)
			dm.mutex.Lock()
			delete(dm.downloads, download.ID)
			delete(dm.progressChannels, download.ID)
			dm.mutex.Unlock()
			return firstDownload, nil
		}
	}

	return firstDownload, nil
}

// GetDownload returns a snapshot copy of a download. Copies are returned so
// callers can read or encode them without racing progress updates, which
// mutate the live objects under the manager mutex.
func (dm *DownloadManager) GetDownload(id string) (*core.Download, bool) {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	download, exists := dm.downloads[id]
	if !exists {
		return nil, false
	}
	snapshot := *download
	return &snapshot, true
}

// GetAllDownloads returns snapshot copies of all downloads, sorted oldest
// first so API responses are stable across polls.
func (dm *DownloadManager) GetAllDownloads() []*core.Download {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	downloads := make([]*core.Download, 0, len(dm.downloads))
	for _, download := range dm.downloads {
		snapshot := *download
		downloads = append(downloads, &snapshot)
	}
	sort.Slice(downloads, func(i, j int) bool {
		return downloads[i].CreatedAt.Before(downloads[j].CreatedAt)
	})

	return downloads
}

func (dm *DownloadManager) CancelDownload(id string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	download, exists := dm.downloads[id]
	if !exists {
		return fmt.Errorf("download not found")
	}

	if download.Status == core.StatusDownloading || download.Status == core.StatusPostProcessing || download.Status == core.StatusConverting {
		// Cancel the actual download or post-processing process
		if cancelFunc, exists := dm.cancelFuncs[id]; exists {
			log.Printf("[MANAGER] Canceling %s process for download %s", download.Status, id)
			cancelFunc()
			delete(dm.cancelFuncs, id)
		}

		// Clean up temporary files immediately when canceling post-processing
		if download.Status == core.StatusPostProcessing {
			log.Printf("[MANAGER] Cleaning up temporary files for canceled post-processing download %s", id)
			dm.cleanupTemporaryFiles(download)
		}

		download.Status = core.StatusCanceled
	} else if download.Status == core.StatusQueued {
		download.Status = core.StatusCanceled
	}

	// Clean up processing URL on cancelation
	delete(dm.processingUrls, download.URL)

	return nil
}

func (dm *DownloadManager) PauseDownload(id string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	download, exists := dm.downloads[id]
	if !exists {
		return fmt.Errorf("download not found")
	}

	if download.Status == core.StatusDownloading {
		// Cancel the actual download process (this will stop yt-dlp)
		if cancelFunc, exists := dm.cancelFuncs[id]; exists {
			cancelFunc()
			delete(dm.cancelFuncs, id)
		}
		download.Status = core.StatusPaused
		dm.pausedDownloads[id] = download
		log.Printf("[MANAGER] Download %s paused", id)
	} else if download.Status == core.StatusQueued {
		download.Status = core.StatusPaused
		dm.pausedDownloads[id] = download
		log.Printf("[MANAGER] Download %s paused (was queued)", id)
	} else {
		return fmt.Errorf("download cannot be paused in current state: %s", download.Status)
	}

	return nil
}

func (dm *DownloadManager) ResumeDownload(id string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	download, exists := dm.downloads[id]
	if !exists {
		return fmt.Errorf("download not found")
	}

	if download.Status != core.StatusPaused {
		return fmt.Errorf("download is not paused")
	}

	// Reset download state and re-queue (yt-dlp will detect partial files and resume)
	download.Status = core.StatusQueued
	download.Error = ""
	// Don't reset CompletedAt as it hasn't completed yet
	// Don't reset progress as yt-dlp will show correct progress when resuming

	// Remove from paused downloads
	delete(dm.pausedDownloads, id)

	// Re-queue the download
	select {
	case dm.queue <- download:
		log.Printf("[MANAGER] Download %s resumed (will continue from partial file if exists)", id)
		return nil
	default:
		download.Status = core.StatusPaused
		dm.pausedDownloads[id] = download
		return fmt.Errorf("download queue is full")
	}
}

func (dm *DownloadManager) RetryDownload(id string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	download, exists := dm.downloads[id]
	if !exists {
		return fmt.Errorf("download not found")
	}

	if download.Status == core.StatusDownloading || download.Status == core.StatusQueued {
		return fmt.Errorf("download is already active")
	}

	// Reset download state
	download.Status = core.StatusQueued
	download.Error = ""
	download.Progress = core.DownloadProgress{}
	download.CompletedAt = nil

	// Re-queue the download
	select {
	case dm.queue <- download:
		return nil
	default:
		return fmt.Errorf("download queue is full")
	}
}

func (dm *DownloadManager) RemoveDownload(id string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	download, exists := dm.downloads[id]
	if !exists {
		return fmt.Errorf("download not found")
	}

	if download.Status == core.StatusDownloading ||
		download.Status == core.StatusPostProcessing ||
		download.Status == core.StatusConverting {
		// Cancel the download, post-processing, or convert first. Cancelling a
		// convert makes ffmpeg exit, and ConvertToH264AAC removes its temp file.
		if cancelFunc, exists := dm.cancelFuncs[id]; exists {
			log.Printf("[MANAGER] Canceling running process for download %s", id)
			cancelFunc()
			delete(dm.cancelFuncs, id)
		}
	}

	// Delete the actual file if it exists (for completed, already exists,
	// post-processing, or converting downloads)
	if download.Status == core.StatusCompleted ||
		download.Status == core.StatusAlreadyExists ||
		download.Status == core.StatusPostProcessing ||
		download.Status == core.StatusConverting {
		if download.OutputPath != "" {
			// Delete the main output file
			if err := os.Remove(download.OutputPath); err != nil {
				log.Printf("[MANAGER] Failed to delete file %s: %v", download.OutputPath, err)
				// Don't return error here - we still want to remove from tracking even if file deletion fails
			} else {
				log.Printf("[MANAGER] Successfully deleted file: %s", download.OutputPath)
			}
		}
	}

	// Clean up temporary files for downloads that were in progress, post-processing, or left in failed/canceled state
	if download.Status == core.StatusDownloading ||
		download.Status == core.StatusPostProcessing ||
		download.Status == core.StatusFailed ||
		download.Status == core.StatusCanceled {
		dm.cleanupTemporaryFiles(download)
		log.Printf("[MANAGER] Cleaned up temporary files for download %s (status: %s)", download.ID, download.Status)

		// Also try to delete any partial output file that might exist for downloading/failed downloads
		if (download.Status == core.StatusDownloading || download.Status == core.StatusFailed) && download.OutputPath != "" {
			if err := os.Remove(download.OutputPath); err != nil {
				// File might not exist yet or might be a temp file, which is fine
				log.Printf("[MANAGER] Note: Could not delete partial file %s: %v", download.OutputPath, err)
			} else {
				log.Printf("[MANAGER] Successfully deleted partial file: %s", download.OutputPath)
			}
		}
	}

	delete(dm.downloads, id)
	delete(dm.pausedDownloads, id)
	delete(dm.cancelFuncs, id)              // Ensure cancel function is removed
	delete(dm.processingUrls, download.URL) // Clean up processing URL
	if ch, exists := dm.progressChannels[id]; exists {
		// Safe channel closing - use recover to handle already closed channels
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel was already closed, ignore the panic
					log.Printf("[MANAGER] Recovered from closing already closed channel: %v", r)
				}
			}()
			close(ch)
		}()
		delete(dm.progressChannels, id)
	}

	return nil
}

// cleanupTemporaryFiles removes temporary files created during post-processing
func (dm *DownloadManager) cleanupTemporaryFiles(download *core.Download) {
	if download.Filename == "" && download.Title == "" {
		log.Printf("[MANAGER] Cleanup: No filename or title available for download %s", download.ID)
		return
	}

	// Extract the base filename for searching - prioritize OutputPath if available
	baseFilename := download.Filename
	if download.OutputPath != "" {
		// Use the actual output filename as the most accurate base
		outputFile := filepath.Base(download.OutputPath)
		if outputFile != "" {
			baseFilename = outputFile
		}
	}
	if baseFilename == "" && download.Title != "" {
		// Sanitize title to match potential filename patterns
		baseFilename = strings.ToLower(strings.ReplaceAll(download.Title, " ", "_"))
		baseFilename = strings.ReplaceAll(baseFilename, "/", "_")
		baseFilename = strings.ReplaceAll(baseFilename, "\\", "_")
	}

	if baseFilename == "" {
		log.Printf("[MANAGER] Cleanup: No base filename derived for download %s", download.ID)
		return
	}

	// Remove file extension to get base name
	if ext := filepath.Ext(baseFilename); ext != "" {
		baseFilename = strings.TrimSuffix(baseFilename, ext)
	}

	// Look for temporary files in the output directory
	outputDir := dm.outputDir
	if download.OutputPath != "" {
		outputDir = filepath.Dir(download.OutputPath)
	}

	files, err := os.ReadDir(outputDir)
	if err != nil {
		log.Printf("[MANAGER] Failed to read output directory %s: %v", outputDir, err)
		return
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filename := file.Name()

		// Only ever touch files that belong to this download: they must start
		// with the download's base filename. Never delete unrelated files.
		if !strings.HasPrefix(strings.ToLower(filename), strings.ToLower(baseFilename)) {
			continue
		}

		if isTemporaryArtifact(filename) {
			fullPath := filepath.Join(outputDir, filename)
			if err := os.Remove(fullPath); err != nil {
				log.Printf("[MANAGER] Failed to delete temp file %s: %v", fullPath, err)
			} else {
				log.Printf("[MANAGER] Cleaned up temp file: %s", fullPath)
			}
		}
	}
}

// temp file suffixes produced by yt-dlp and ffmpeg during downloads
var tempArtifactSuffixes = []string{
	".part", ".ytdl", ".tmp", ".temp", ".download", ".downloading", ".partial",
}

// fragment markers that appear mid-filename, e.g. "title.mp4.part-Frag42"
var tempArtifactMarkers = []string{
	".part-", ".ytdl.",
}

// format-stream segment like ".f137." or ".f140." in yt-dlp intermediate files
var formatStreamSegment = regexp.MustCompile(`\.f\d+\.`)

// isTemporaryArtifact reports whether a filename is a yt-dlp or ffmpeg
// intermediate file that is safe to delete once its download is removed.
// Matching is deliberately strict: exact suffixes, fragment markers, or a
// ".f<digits>." stream segment. Substring checks like ".f" are not used
// because they match legitimate files (".flac", ".flv").
func isTemporaryArtifact(filename string) bool {
	lower := strings.ToLower(filename)
	for _, suffix := range tempArtifactSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, marker := range tempArtifactMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return formatStreamSegment.MatchString(lower)
}

// ClearAllQueued marks all downloads in queued status as canceled and removes them
func (dm *DownloadManager) ClearAllQueued() error {
	func() {
		dm.mutex.Lock()
		defer dm.mutex.Unlock()

		canceledCount := 0
		deletedCount := 0

		for id, download := range dm.downloads {
			if download.Status == core.StatusQueued {
				// First mark as canceled so workers will skip them when they pick them up from the queue
				download.Status = core.StatusCanceled
				canceledCount++

				// Clean up from processing URLs map to allow re-adding same URL
				delete(dm.processingUrls, download.URL)

				// Remove progress channels and cancel functions
				delete(dm.pausedDownloads, id)
				delete(dm.cancelFuncs, id)
				if ch, exists := dm.progressChannels[id]; exists {
					func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("[MANAGER] Channel already closed during cleanup for download %s", id)
							}
						}()
						close(ch)
					}()
					delete(dm.progressChannels, id)
				}

				// Remove from downloads map completely
				delete(dm.downloads, id)
				deletedCount++
			}
		}

		log.Printf("[MANAGER] Canceled %d queued downloads, deleted %d from tracking", canceledCount, deletedCount)
	}()

	// Save state immediately to persist the cleared queue (outside of mutex)
	if err := dm.SaveState(); err != nil {
		log.Printf("[MANAGER] Warning: Failed to save state after clearing queue: %v", err)
	}

	// Start a goroutine to drain any remaining items from the queue
	go dm.drainQueuedItems()

	return nil
}

// drainQueuedItems helps drain canceled items from the queue more quickly
func (dm *DownloadManager) drainQueuedItems() {
	// Give a short timeout for draining to avoid hanging
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	drained := 0
	for {
		select {
		case download := <-dm.queue:
			// Check if this download was canceled/deleted
			dm.mutex.RLock()
			_, exists := dm.downloads[download.ID]
			dm.mutex.RUnlock()

			if !exists || download.Status == core.StatusCanceled {
				// This download was cleared, just discard it
				drained++
				log.Printf("[MANAGER] Drained canceled download from queue: %s", download.ID)
				continue
			} else {
				// This download is still valid, put it back in the queue
				select {
				case dm.queue <- download:
					// Successfully put it back
				default:
					// Queue is full, worker will pick it up later
				}
				// Stop draining since we found a valid item
				log.Printf("[MANAGER] Queue draining stopped after removing %d canceled items", drained)
				return
			}
		case <-timeout.C:
			// Timeout reached, stop draining
			log.Printf("[MANAGER] Queue draining completed with timeout after removing %d canceled items", drained)
			return
		default:
			// No more items in queue
			log.Printf("[MANAGER] Queue draining completed after removing %d canceled items", drained)
			return
		}
	}
}

// DeleteAllCompleted removes all completed downloads and their files
func (dm *DownloadManager) DeleteAllCompleted() error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	deletedCount := 0
	filesDeleted := 0
	for id, download := range dm.downloads {
		if download.Status == core.StatusCompleted || download.Status == core.StatusAlreadyExists {
			// Delete the actual file if it exists
			if download.OutputPath != "" {
				if err := os.Remove(download.OutputPath); err != nil {
					log.Printf("[MANAGER] Failed to delete file %s: %v", download.OutputPath, err)
				} else {
					filesDeleted++
					log.Printf("[MANAGER] Deleted file: %s", download.OutputPath)
				}
			}

			// Remove from tracking
			delete(dm.downloads, id)
			delete(dm.pausedDownloads, id)
			delete(dm.cancelFuncs, id)
			if ch, exists := dm.progressChannels[id]; exists {
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[MANAGER] Channel already closed during completed cleanup for download %s", id)
						}
					}()
					close(ch)
				}()
				delete(dm.progressChannels, id)
			}
			deletedCount++
		}
	}

	log.Printf("[MANAGER] Deleted %d completed downloads and %d files", deletedCount, filesDeleted)
	return nil
}

// ClearAllFailed removes all failed downloads from the list
func (dm *DownloadManager) ClearAllFailed() error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	clearedCount := 0
	for id, download := range dm.downloads {
		if download.Status == core.StatusFailed {
			// Clean up any temporary files that might have been left behind
			dm.cleanupTemporaryFiles(download)

			// Remove from tracking
			delete(dm.downloads, id)
			delete(dm.pausedDownloads, id)
			delete(dm.cancelFuncs, id)
			if ch, exists := dm.progressChannels[id]; exists {
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[MANAGER] Channel already closed during failed cleanup for download %s", id)
						}
					}()
					close(ch)
				}()
				delete(dm.progressChannels, id)
			}
			clearedCount++
		}
	}

	log.Printf("[MANAGER] Cleared %d failed downloads and cleaned up temporary files", clearedCount)
	return nil
}

func (dm *DownloadManager) GetProgress(id string) (chan core.DownloadProgress, bool) {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	ch, exists := dm.progressChannels[id]
	return ch, exists
}

func (dm *DownloadManager) worker() {
	dm.mutex.Lock()
	dm.activeWorkers++
	workerID := dm.activeWorkers
	dm.mutex.Unlock()

	if utils.VerboseLogging {
		utils.LogDebugf("[MANAGER] Worker %d started", workerID)
	}
	defer func() {
		dm.mutex.Lock()
		dm.activeWorkers--
		dm.mutex.Unlock()
		log.Printf("[MANAGER] Worker %d shutting down", workerID)
	}()

	for {
		select {
		case <-dm.workerCtx.Done():
			return
		case <-dm.workerQuit:
			// Targeted shrink: this worker stops while the others keep running.
			return
		case download := <-dm.queue:
			// Check if the download was canceled or removed while in queue
			dm.mutex.RLock()
			currentDownload, exists := dm.downloads[download.ID]
			dm.mutex.RUnlock()

			if !exists {
				log.Printf("[MANAGER] Skipping download %s - no longer exists (was cleared)", download.ID)
				continue
			}

			if currentDownload.Status == core.StatusCanceled {
				log.Printf("[MANAGER] Skipping canceled download %s", download.ID)
				continue
			}
			if currentDownload.Status == core.StatusPaused {
				log.Printf("[MANAGER] Skipping paused download %s", download.ID)
				continue
			}

			// Use the current download state, not the queued one
			log.Printf("[MANAGER] Worker processing download %s", currentDownload.ID)
			dm.processDownload(currentDownload)
			// After the download fully settles (cancel funcs and progress
			// channel cleaned up), optionally re-encode for compatibility.
			dm.maybeAutoReencode(currentDownload.ID)
		}
	}
}

// startWorkers starts the specified number of worker goroutines
func (dm *DownloadManager) startWorkers(count int) {
	for i := 0; i < count; i++ {
		go dm.worker()
	}
}

// UpdateConfig updates the download manager configuration and adjusts workers accordingly
func (dm *DownloadManager) UpdateConfig(newConfig *config.Config) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	oldMaxConcurrent := dm.maxConcurrent
	dm.config = newConfig
	dm.maxConcurrent = newConfig.MaxConcurrentDownloads
	dm.outputDir = newConfig.DownloadPath

	// Create a new downloader with updated paths
	dm.downloader = core.NewDownloader(
		newConfig.YtDlpPath,
		newConfig.FfmpegPath,
		newConfig.CookiesFilePath,
		newConfig.EnableHardwareAccel,
		newConfig.OptimizeForLowPower)
	dm.downloader.SetTimeouts(
		time.Duration(newConfig.DownloadStartTimeoutSeconds)*time.Second,
		time.Duration(newConfig.PlaylistLoadTimeoutSeconds)*time.Second)

	log.Printf("[MANAGER] Config updated: MaxConcurrent %d -> %d, OutputDir -> %s",
		oldMaxConcurrent, dm.maxConcurrent, dm.outputDir)
	log.Printf("[MANAGER] Downloader updated with new paths: yt-dlp=%s, ffmpeg=%s",
		newConfig.YtDlpPath, newConfig.FfmpegPath)

	// Adjust workers if needed
	if oldMaxConcurrent != dm.maxConcurrent {
		dm.adjustWorkers(oldMaxConcurrent, dm.maxConcurrent)
	}
}

// adjustWorkers adjusts the number of worker goroutines
func (dm *DownloadManager) adjustWorkers(oldCount, newCount int) {
	if newCount > oldCount {
		// Start additional workers
		additional := newCount - oldCount
		log.Printf("[MANAGER] Starting %d additional workers", additional)
		dm.startWorkers(additional)
	} else if newCount < oldCount {
		// Reduce workers by signalling exactly (oldCount-newCount) workers to
		// stop after they finish any in-flight job. The previous approach
		// cancelled all workers and started newCount fresh ones, so the still
		// finishing old workers plus the new ones briefly exceeded the limit
		// (finding Q-2). Signalling individual workers keeps the live count at
		// or below oldCount and converges to newCount with no new workers.
		toStop := oldCount - newCount
		log.Printf("[MANAGER] Reducing workers from %d to %d", oldCount, newCount)

		// Send asynchronously: workers busy in processDownload are not at the
		// select, and adjustWorkers runs under dm.mutex which those workers
		// also need, so a synchronous send here would deadlock. Abort the sends
		// if the manager shuts down to avoid leaking this goroutine.
		go func() {
			for i := 0; i < toStop; i++ {
				select {
				case dm.workerQuit <- struct{}{}:
				case <-dm.ctx.Done():
					return
				}
			}
		}()
	}
}

func (dm *DownloadManager) processDownload(download *core.Download) {
	log.Printf("[MANAGER] Processing download %s", download.ID)

	dm.mutex.Lock()
	// Update status to downloading immediately
	download.Status = core.StatusDownloading
	progressChan := dm.progressChannels[download.ID]
	dm.mutex.Unlock()

	req := core.DownloadRequest{
		URL:       download.URL,
		Type:      download.Type,
		Quality:   download.Quality,
		Format:    download.Format,
		OutputDir: dm.outputDir, // Use the configured output directory
	}

	log.Printf("[MANAGER] Download %s: Creating context and starting download", download.ID)

	// Create a context for this specific download
	ctx, cancel := context.WithCancel(dm.ctx)

	// Store cancel function for potential cancelation
	dm.mutex.Lock()
	dm.cancelFuncs[download.ID] = cancel
	dm.mutex.Unlock()

	defer func() {
		dm.mutex.Lock()
		delete(dm.cancelFuncs, download.ID)
		delete(dm.processingUrls, download.URL)
		dm.mutex.Unlock()
		cancel()
		log.Printf("[MANAGER] Download %s: Context canceled and cleanup completed", download.ID)
	}()

	// Start progress monitoring goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[MANAGER] Download %s: Progress monitoring goroutine recovered from panic: %v", download.ID, r)
			}
		}()
		for progress := range progressChan {
			dm.mutex.Lock()
			download.Progress = progress
			dm.mutex.Unlock()
		}
		log.Printf("[MANAGER] Download %s: Progress monitoring stopped", download.ID)
	}()

	// Start download
	log.Printf("[MANAGER] Download %s: Calling downloader.Download", download.ID)
	completedDownload, err := dm.downloader.Download(
		ctx, req, progressChan, dm.UpdateDownloadTitle, dm.UpdateDownloadStatus, download.ID)

	dm.mutex.Lock()
	if ctx.Err() == context.Canceled {
		log.Printf("[MANAGER] Download %s: Canceled", download.ID)
		download.Status = core.StatusCanceled
	} else if err != nil {
		log.Printf("[MANAGER] Download %s: Failed with error: %v", download.ID, err)
		download.Status = core.StatusFailed
		download.Error = err.Error()
	} else {
		log.Printf("[MANAGER] Download %s: Completed successfully", download.ID)
		// Log current status before overwriting
		log.Printf("[MANAGER] Download %s: Current status before completion: %s", download.ID, download.Status)
		log.Printf("[MANAGER] Download %s: Returned status from downloader: %s", download.ID, completedDownload.Status)

		download.Status = completedDownload.Status
		download.Title = completedDownload.Title
		download.Filename = completedDownload.Filename
		download.OutputPath = completedDownload.OutputPath
		download.FileSize = completedDownload.FileSize
		download.CompletedAt = completedDownload.CompletedAt

		if download.Type == core.VideoDownload && download.OutputPath != "" {
			if v, a, perr := dm.downloader.ProbeCodecs(download.OutputPath); perr != nil {
				log.Printf("[MANAGER] Download %s: codec probe failed: %v", download.ID, perr)
			} else {
				download.VideoCodec = v
				download.AudioCodec = a
			}
		}

		log.Printf("[MANAGER] Download %s: Final status after completion: %s", download.ID, download.Status)
	}

	// Close progress channel safely after download completion
	if ch, exists := dm.progressChannels[download.ID]; exists {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel was already closed, ignore the panic
					// This is expected behavior when multiple goroutines try to close the same channel
					log.Printf("Progress channel for download %s was already closed", download.ID)
				}
			}()
			close(ch)
		}()
		delete(dm.progressChannels, download.ID)
	}
	dm.mutex.Unlock()
}

// videoIsCompatible reports whether a download is already H.264 + AAC.
func videoIsCompatible(d *core.Download) bool {
	return strings.EqualFold(d.VideoCodec, "h264") && strings.EqualFold(d.AudioCodec, "aac")
}

// maybeAutoReencode implements the "Re-encode video for compatibility" setting
// conditionally: when enabled, a completed video that is not already H.264 + AAC
// is transcoded via the same convert pipeline, while already-compatible files
// are left untouched so no time is wasted re-encoding them. Codecs are probed
// here if the completion path did not already record them.
func (dm *DownloadManager) maybeAutoReencode(id string) {
	dm.mutex.RLock()
	cfg := dm.config
	d, ok := dm.downloads[id]
	gate := ok && cfg != nil && cfg.ReencodeForCompatibility &&
		d.Type == core.VideoDownload &&
		(d.Status == core.StatusCompleted || d.Status == core.StatusAlreadyExists) &&
		!d.Converted
	var path, vcodec, acodec string
	if gate {
		path, vcodec, acodec = d.OutputPath, d.VideoCodec, d.AudioCodec
	}
	dm.mutex.RUnlock()
	if !gate || path == "" {
		return
	}

	// Probe if codecs are unknown (e.g. an already-existing file that the
	// completion path never probed), so we only convert when truly needed.
	if vcodec == "" && acodec == "" {
		if v, a, err := dm.downloader.ProbeCodecs(path); err == nil {
			vcodec, acodec = v, a
			dm.mutex.Lock()
			if cur, ok := dm.downloads[id]; ok {
				cur.VideoCodec, cur.AudioCodec = v, a
			}
			dm.mutex.Unlock()
		}
	}

	if strings.EqualFold(vcodec, "h264") && strings.EqualFold(acodec, "aac") {
		return // already compatible; nothing to do
	}

	log.Printf("[MANAGER] %s: auto re-encoding for compatibility (%s/%s -> h264/aac)", id, vcodec, acodec)
	if err := dm.ConvertDownload(id); err != nil {
		log.Printf("[MANAGER] %s: auto re-encode skipped: %v", id, err)
	}
}

// ConvertDownload transcodes a single completed video to H.264 + AAC MP4 in a
// background goroutine, replacing the original file. The job is cancelable via
// the existing CancelDownload path (it registers under cancelFuncs[id]).
func (dm *DownloadManager) ConvertDownload(id string) error {
	dm.mutex.Lock()
	download, exists := dm.downloads[id]
	if !exists {
		dm.mutex.Unlock()
		return fmt.Errorf("download not found")
	}
	if download.Type != core.VideoDownload {
		dm.mutex.Unlock()
		return fmt.Errorf("only video downloads can be converted")
	}
	if download.Status != core.StatusCompleted && download.Status != core.StatusAlreadyExists {
		dm.mutex.Unlock()
		return fmt.Errorf("download must be completed before converting")
	}
	if download.Converted || videoIsCompatible(download) {
		dm.mutex.Unlock()
		return fmt.Errorf("download is already H.264 + AAC")
	}
	if download.OutputPath == "" {
		dm.mutex.Unlock()
		return fmt.Errorf("download has no output file")
	}

	ctx, cancel := context.WithCancel(dm.ctx)
	dm.cancelFuncs[id] = cancel
	progressChan := make(chan core.DownloadProgress, 10)
	dm.progressChannels[id] = progressChan
	prevStatus := download.Status
	download.Status = core.StatusConverting
	download.Error = ""
	download.Progress = core.DownloadProgress{}
	dm.mutex.Unlock()

	go dm.runConvert(ctx, cancel, id, download, prevStatus, progressChan)
	return nil
}

// runConvert performs the transcode and updates state. Bounded by convertSem so
// a burst of converts does not overwhelm the machine.
func (dm *DownloadManager) runConvert(
	ctx context.Context,
	cancel context.CancelFunc,
	id string,
	download *core.Download,
	prevStatus core.DownloadStatus,
	progressChan chan core.DownloadProgress,
) {
	defer func() {
		// Cancel the context first so the progress monitor stops sending before
		// the channel is closed, then close the channel.
		cancel()
		dm.mutex.Lock()
		delete(dm.cancelFuncs, id)
		if ch, ok := dm.progressChannels[id]; ok {
			func() {
				defer func() { _ = recover() }()
				close(ch)
			}()
			delete(dm.progressChannels, id)
		}
		dm.mutex.Unlock()
	}()

	go func() {
		defer func() { _ = recover() }()
		for p := range progressChan {
			dm.mutex.Lock()
			download.Progress = p
			dm.mutex.Unlock()
		}
	}()

	select {
	case dm.convertSem <- struct{}{}:
		defer func() { <-dm.convertSem }()
	case <-ctx.Done():
		dm.finishConvert(id, prevStatus, "", context.Canceled)
		return
	}

	newPath, err := dm.downloader.ConvertToH264AAC(ctx, download, progressChan)
	dm.finishConvert(id, prevStatus, newPath, err)
}

// finishConvert applies the terminal state of a convert job and saves state.
func (dm *DownloadManager) finishConvert(id string, prevStatus core.DownloadStatus, newPath string, err error) {
	// Probe the converted file outside the lock: ffprobe can block for up to its
	// timeout, and we must not hold dm.mutex (which gates polling and other
	// actions) for that long.
	var newV, newA string
	var probed bool
	if err == nil && newPath != "" {
		if v, a, perr := dm.downloader.ProbeCodecs(newPath); perr == nil {
			newV, newA, probed = v, a, true
		}
	}

	dm.mutex.Lock()
	download, exists := dm.downloads[id]
	if !exists {
		dm.mutex.Unlock()
		return
	}
	switch {
	case err == context.Canceled:
		log.Printf("[MANAGER] Convert %s canceled", id)
		download.Status = prevStatus
	case err != nil:
		log.Printf("[MANAGER] Convert %s failed: %v", id, err)
		download.Status = prevStatus
		download.Error = err.Error()
	default:
		download.OutputPath = newPath
		download.Filename = filepath.Base(newPath)
		download.Converted = true
		download.Status = prevStatus
		download.Progress = core.DownloadProgress{}
		if probed {
			download.VideoCodec = newV
			download.AudioCodec = newA
		}
		log.Printf("[MANAGER] Convert %s completed: %s", id, newPath)
	}
	dm.mutex.Unlock()
	if serr := dm.SaveState(); serr != nil {
		log.Printf("[MANAGER] Failed to save state after convert %s: %v", id, serr)
	}
}

func (dm *DownloadManager) UpdateDownloadTitle(id, title string) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	if download, exists := dm.downloads[id]; exists {
		if download.Title != title {
			download.Title = title
			log.Printf("[MANAGER] Download %s: Title updated to: %s", id, title)
		}
	}
}

// UpdateDownloadOutputPath sets a download's output path under the manager
// lock. Used by tests to point a record at a fixture file safely.
func (dm *DownloadManager) UpdateDownloadOutputPath(id, path string) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	if download, exists := dm.downloads[id]; exists {
		download.OutputPath = path
	}
}

func (dm *DownloadManager) UpdateDownloadStatus(id string, status core.DownloadStatus) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	if download, exists := dm.downloads[id]; exists {
		if download.Status != status {
			log.Printf("[MANAGER] Download %s: Status updating from %s to %s", id, download.Status, status)
			download.Status = status
			log.Printf("[MANAGER] Download %s: Status updated to: %s", id, status)
		}
	} else {
		log.Printf("[MANAGER] Download %s: Not found when trying to update status to %s", id, status)
	}
}

func (dm *DownloadManager) cleanupWorker() {
	// Run cleanup every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-dm.ctx.Done():
			log.Printf("[MANAGER] Cleanup worker shutting down")
			return
		case <-ticker.C:
			dm.cleanupExpiredDownloads()
		}
	}
}

func (dm *DownloadManager) cleanupExpiredDownloads() {
	if dm.config.CompletedFileExpiryHours <= 0 {
		return // Auto-expiry disabled
	}

	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	expiryDuration := time.Duration(dm.config.CompletedFileExpiryHours) * time.Hour
	now := time.Now()
	deletedCount := 0

	for id, download := range dm.downloads {
		if download.Status == core.StatusCompleted && download.CompletedAt != nil {
			timeSinceCompletion := now.Sub(*download.CompletedAt)
			if timeSinceCompletion > expiryDuration {
				// Delete the actual file
				if download.OutputPath != "" {
					if err := os.Remove(download.OutputPath); err != nil {
						log.Printf("[MANAGER] Failed to delete expired file %s: %v", download.OutputPath, err)
					} else {
						log.Printf("[MANAGER] Deleted expired file: %s", download.OutputPath)
					}
				}

				// Remove from downloads map
				delete(dm.downloads, id)

				// Clean up progress channel
				if ch, exists := dm.progressChannels[id]; exists {
					func() {
						defer func() {
							if r := recover(); r != nil {
								// Channel was already closed, ignore the panic
								// This is expected behavior when multiple goroutines try to close the same channel
								log.Printf("Progress channel for download %s was already closed", id)
							}
						}()
						close(ch)
					}()
					delete(dm.progressChannels, id)
				}

				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		log.Printf("[MANAGER] Cleanup completed: removed %d expired downloads", deletedCount)
	}
}

// CheckFileExistence checks if a file for the given download request already exists (exported for API use)
func (dm *DownloadManager) CheckFileExistence(req core.DownloadRequest) string {
	return dm.checkFileExists(req)
}

// checkFileExists checks if a file for the given download request already exists
func (dm *DownloadManager) checkFileExists(req core.DownloadRequest) string {
	// Try to get video info to determine potential filename
	info, err := dm.downloader.GetVideoInfo(req.URL)
	if err != nil {
		// If we can't get video info, we can't check for duplicates effectively
		return ""
	}

	// Determine expected file extension
	expectedExt := "." + req.Format

	// List of potential filenames to check
	potentialFilenames := []string{
		core.SanitizeFilename(info.Title) + expectedExt,
		info.Title + expectedExt,
		strings.ReplaceAll(info.Title, " ", "_") + expectedExt,
		strings.ToLower(core.SanitizeFilename(info.Title)) + expectedExt,
	}

	// Check each potential filename
	for _, filename := range potentialFilenames {
		fullPath := filepath.Join(dm.outputDir, filename)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}

	// Also check for similar files in the directory
	files, err := os.ReadDir(dm.outputDir)
	if err != nil {
		return ""
	}

	titleWords := strings.Fields(strings.ToLower(info.Title))
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filename := strings.ToLower(file.Name())
		// Check if filename has correct extension and contains most title words
		if strings.HasSuffix(filename, expectedExt) {
			matchCount := 0
			significantWords := 0
			for _, word := range titleWords {
				if len(word) <= 3 { // Only significant words (>3 chars) are comparable
					continue
				}
				significantWords++
				if strings.Contains(filename, word) {
					matchCount++
				}
			}
			// If more than 60% of the significant words match, consider it a duplicate
			if significantWords > 0 && float64(matchCount)/float64(significantWords) > 0.6 {
				return filepath.Join(dm.outputDir, file.Name())
			}
		}
	}

	return ""
}

// extractTitleFromPath extracts a readable title from a file path
func (dm *DownloadManager) extractTitleFromPath(filePath string) string {
	filename := filepath.Base(filePath)
	// Remove extension
	title := strings.TrimSuffix(filename, filepath.Ext(filename))
	// Replace underscores with spaces
	title = strings.ReplaceAll(title, "_", " ")
	// Basic cleanup
	title = strings.TrimSpace(title)

	if title == "" {
		title = filename
	}

	return title
}

func (dm *DownloadManager) Shutdown() {
	log.Printf("[MANAGER] Shutting down download manager...")

	// Save final state
	if err := dm.SaveState(); err != nil {
		log.Printf("[MANAGER] Failed to save final state: %v", err)
	}

	// Cancel worker context first to stop workers gracefully
	dm.workerCancel()

	// Then cancel main context. The queue channel is intentionally not
	// closed: workers select on workerCtx.Done() and a closed channel would
	// deliver nil downloads to any worker still in its receive case.
	dm.cancel()

	log.Printf("[MANAGER] Download manager shutdown complete")
}
