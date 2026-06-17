package manager

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"goyt/internal/core"
)

// StateFile represents the persisted download manager state
type StateFile struct {
	Downloads map[string]*core.Download `json:"downloads"`
	SavedAt   time.Time                 `json:"saved_at"`
	Version   string                    `json:"version"`
}

const StateVersion = "1.0"

// GetStateFilePath returns the path where the state file should be stored
func (dm *DownloadManager) GetStateFilePath() string {
	return filepath.Join(dm.outputDir, ".goyt_state.json")
}

// SaveState persists the current download manager state to disk
func (dm *DownloadManager) SaveState() error {
	stateFile := StateFile{
		Downloads: make(map[string]*core.Download),
		SavedAt:   time.Now(),
		Version:   StateVersion,
	}

	// Marshal under the lock so the snapshot is consistent, then release before
	// touching disk: holding the read lock across file I/O would block every
	// status and progress write for the duration of the write, which recurs on
	// the 30s save timer (finding FIND-2). Deep-copy each record so the bytes are
	// stable even though workers keep mutating the live objects after unlock.
	dm.mutex.RLock()
	for id, download := range dm.downloads {
		snapshot := *download
		stateFile.Downloads[id] = &snapshot
	}
	dm.mutex.RUnlock()

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	stateFilePath := dm.GetStateFilePath()

	// Write to temp file first, then rename for atomic operation
	tempPath := stateFilePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	if err := os.Rename(tempPath, stateFilePath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	log.Printf("[MANAGER] State saved with %d downloads", len(stateFile.Downloads))
	return nil
}

// LoadState restores the download manager state from disk
func (dm *DownloadManager) LoadState() error {
	stateFilePath := dm.GetStateFilePath()

	// Check if state file exists
	if _, err := os.Stat(stateFilePath); os.IsNotExist(err) {
		log.Printf("[MANAGER] No state file found, starting fresh")
		return nil
	}

	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var stateFile StateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		return fmt.Errorf("failed to unmarshal state file: %w", err)
	}

	// Version compatibility check
	if stateFile.Version != StateVersion {
		log.Printf("[MANAGER] State file version mismatch (found %s, expected %s), starting fresh",
			stateFile.Version, StateVersion)
		return nil
	}

	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	// Restore downloads
	restoredCount := 0
	for id, download := range stateFile.Downloads {
		// Validate download state and file existence
		if dm.validateRestoredDownload(download) {
			dm.downloads[id] = download
			dm.progressChannels[id] = make(chan core.DownloadProgress, 10)

			// Re-queue interrupted downloads
			if download.Status == core.StatusDownloading ||
				download.Status == core.StatusQueued ||
				download.Status == core.StatusPostProcessing {
				download.Status = core.StatusQueued
				select {
				case dm.queue <- download:
					log.Printf("[MANAGER] Re-queued interrupted download: %s", download.Title)
				default:
					log.Printf("[MANAGER] Queue full, marking download as failed: %s", download.Title)
					download.Status = core.StatusFailed
					download.Error = "Queue full during restoration"
				}
			}
			restoredCount++
		} else {
			log.Printf("[MANAGER] Skipping invalid download during restoration: %s", download.ID)
		}
	}

	log.Printf("[MANAGER] State restored: %d downloads loaded (saved at %s)",
		restoredCount, stateFile.SavedAt.Format("2006-01-02 15:04:05"))

	return nil
}

// validateRestoredDownload checks if a restored download is valid
func (dm *DownloadManager) validateRestoredDownload(download *core.Download) bool {
	if download == nil {
		return false
	}

	// Check required fields
	if download.ID == "" || download.URL == "" {
		return false
	}

	// For completed downloads, verify the file still exists
	if download.Status == core.StatusCompleted && download.OutputPath != "" {
		if _, err := os.Stat(download.OutputPath); os.IsNotExist(err) {
			log.Printf("[MANAGER] Completed download file missing, marking as failed: %s", download.OutputPath)
			download.Status = core.StatusFailed
			download.Error = "Output file not found after restart"
		}
	}

	// Reset progress for non-completed downloads
	if download.Status != core.StatusCompleted && download.Status != core.StatusAlreadyExists {
		download.Progress = core.DownloadProgress{}
	}

	return true
}

// StartPeriodicStateSave begins automatically saving state at regular intervals
func (dm *DownloadManager) StartPeriodicStateSave() {
	go func() {
		ticker := time.NewTicker(30 * time.Second) // Save every 30 seconds
		defer ticker.Stop()

		for {
			select {
			case <-dm.ctx.Done():
				log.Printf("[MANAGER] Stopping periodic state saving")
				return
			case <-ticker.C:
				if err := dm.SaveState(); err != nil {
					log.Printf("[MANAGER] Failed to save state: %v", err)
				}
			}
		}
	}()
}

// CleanupStateFile removes the state file (useful for clean shutdown)
func (dm *DownloadManager) CleanupStateFile() {
	stateFilePath := dm.GetStateFilePath()
	if err := os.Remove(stateFilePath); err != nil && !os.IsNotExist(err) {
		log.Printf("[MANAGER] Failed to remove state file: %v", err)
	} else {
		log.Printf("[MANAGER] State file cleaned up")
	}
}
