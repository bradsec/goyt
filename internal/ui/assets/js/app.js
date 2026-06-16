/**
 * goyt - Main Application Module
 * Modern vanilla JavaScript with ES6 modules
 */

import { DownloadManager } from './modules/download-manager.js';
import { SettingsManager } from './modules/settings-manager.js';
import { UIManager } from './modules/ui-manager.js';
import { ApiClient } from './modules/api-client.js';
class goytApp {
  constructor() {
    this.apiClient = new ApiClient();
    this.uiManager = new UIManager(this.apiClient);
    this.downloadManager = new DownloadManager(this.apiClient, this.uiManager);
    this.settingsManager = new SettingsManager(this.apiClient, this.uiManager);
    
    this.pollInterval = null;
    this.isInitialized = false;
  }

  async init() {
    try {
      console.log('Initializing goyt application...');
      
      // Initialize UI
      this.uiManager.init();
      
      // Initialize managers
      await this.downloadManager.init();
      await this.settingsManager.init();
      
      // Set up event listeners
      this.setupEventListeners();
      
      // Start polling for updates
      this.startPolling();
      
      // Load initial data
      await this.loadInitialData();
      
      this.isInitialized = true;
      console.log('goyt application initialized successfully');
      
    } catch (error) {
      console.error('Failed to initialize application:', error);
      this.uiManager.showNotification('Failed to initialize application', 'error');
    }
  }

  setupEventListeners() {
    // Form submission
    const downloadForm = document.getElementById('download-form');
    if (downloadForm) {
      downloadForm.addEventListener('submit', this.handleDownloadSubmit.bind(this));
    }

    // Initialize submit button as disabled
    const submitButton = document.getElementById('submit-button');
    if (submitButton) {
      submitButton.disabled = true;
    }

    // URL input validation
    const urlInput = document.getElementById('url-input');
    if (urlInput) {
      let validationTimeout;
      urlInput.addEventListener('input', (e) => {
        clearTimeout(validationTimeout);
        const submitBtn = document.getElementById('submit-button');
        if (submitBtn) {
          submitBtn.disabled = true; // Disable while validating
        }
        
        validationTimeout = setTimeout(() => {
          this.handleUrlValidation(e.target.value);
        }, 500);
      });
    }

    // Type selector change
    const typeSelect = document.getElementById('type-select');
    if (typeSelect) {
      typeSelect.addEventListener('change', this.handleTypeChange.bind(this));
      // Initialize on page load
      this.handleTypeChange({ target: typeSelect });
    }

    // Settings toggle
    const settingsToggle = document.getElementById('settings-toggle');
    if (settingsToggle) {
      settingsToggle.addEventListener('click', () => {
        this.settingsManager.togglePanel();
      });
    }

    // Keyboard shortcuts
    document.addEventListener('keydown', this.handleKeyboardShortcuts.bind(this));

    // Page visibility change to optimize polling
    document.addEventListener('visibilitychange', this.handleVisibilityChange.bind(this));

    // Window beforeunload to cleanup
    window.addEventListener('beforeunload', this.cleanup.bind(this));
  }

  async handleDownloadSubmit(event) {
    event.preventDefault();
    
    const formData = new FormData(event.target);
    const downloadData = {
      url: formData.get('url')?.trim(),
      type: formData.get('type') || 'video',
      quality: formData.get('quality') || 'best',
      format: formData.get('format') || 'mp4'
    };

    if (!downloadData.url) {
      this.uiManager.showNotification('Please enter a valid URL', 'warning');
      return;
    }

    try {
      await this.downloadManager.startDownload(downloadData);
      event.target.reset();
      this.uiManager.clearUrlValidation();
    } catch (error) {
      console.error('Download submission failed:', error);
      this.uiManager.showNotification('Failed to start download', 'error');
    }
  }

  async handleUrlValidation(url) {
    const submitButton = document.getElementById('submit-button');
    
    if (!url || url.length < 10) {
      this.uiManager.clearUrlValidation();
      if (submitButton) {
        submitButton.disabled = true;
      }
      return;
    }

    // Escalate the message so a long validation (playlist enumeration can take
    // up to ~60s) does not look frozen.
    this.uiManager.showUrlValidating('Validating URL...');
    const progressTimers = [
      setTimeout(() => this.uiManager.showUrlValidating('Still checking...'), 3000),
      setTimeout(() => this.uiManager.showUrlValidating('Hang tight...'), 10000),
    ];
    const clearProgressTimers = () => progressTimers.forEach(clearTimeout);

    try {
      const validationResult = await this.apiClient.validateUrl(url);
      clearProgressTimers();
      this.uiManager.showUrlValidationResult(validationResult);

      // Validation is advisory only. yt-dlp supports thousands of sites and the
      // info probe can fail for URLs that still download fine (cookies needed,
      // region locks, rate limits, transient errors), so never block the
      // download on it. The server re-checks the request when the download runs.
      if (submitButton) {
        submitButton.disabled = false;
      }
    } catch (error) {
      clearProgressTimers();
      console.error('URL validation failed:', error);
      // Validation is advisory. On a timeout or network error (common on slow
      // links, where playlist enumeration can be slow), tell the user instead
      // of clearing the panel silently, and allow the download attempt anyway
      // (the server validates the request again).
      const message = error.message === 'Request timed out'
        ? 'Validation timed out (slow network). You can still try the download.'
        : 'Could not validate the URL. You can still try the download.';
      this.uiManager.showUrlValidationWarning(message);
      if (submitButton) {
        submitButton.disabled = false;
      }
    }
  }

  handleTypeChange(event) {
    const selectedType = event.target.value;
    const qualityGroup = document.querySelector('#quality-select').closest('.form-group');
    const formatSelect = document.getElementById('format-select');
    
    // Define format options for each type
    const formats = {
      video: [
        { value: 'mp4', label: 'MP4' },
        { value: 'mkv', label: 'MKV' },
        { value: 'webm', label: 'WebM' },
        { value: 'avi', label: 'AVI' }
      ],
      audio: [
        { value: 'mp3', label: 'MP3' },
        { value: 'm4a', label: 'M4A' },
        { value: 'wav', label: 'WAV' },
        { value: 'flac', label: 'FLAC' }
      ]
    };

    // Update format dropdown
    if (formatSelect) {
      const currentValue = formatSelect.value;
      formatSelect.innerHTML = '';
      
      formats[selectedType].forEach(format => {
        const option = document.createElement('option');
        option.value = format.value;
        option.textContent = format.label;
        formatSelect.appendChild(option);
      });
      
      // Try to maintain the current selection if it's valid for the new type
      const validOption = formats[selectedType].find(f => f.value === currentValue);
      if (validOption) {
        formatSelect.value = currentValue;
      } else {
        // Set default format based on settings and type
        const settings = this.settingsManager.settings;
        if (selectedType === 'video' && settings?.default_video_format) {
          formatSelect.value = settings.default_video_format;
        } else if (selectedType === 'audio' && settings?.default_audio_format) {
          formatSelect.value = settings.default_audio_format;
        } else {
          // Fallback defaults
          formatSelect.value = selectedType === 'video' ? 'mp4' : 'mp3';
        }
      }
    }

    // Show/hide quality dropdown based on type
    if (qualityGroup) {
      if (selectedType === 'audio') {
        qualityGroup.style.display = 'none';
        // Set quality to best for audio downloads
        const qualitySelect = document.getElementById('quality-select');
        if (qualitySelect) {
          qualitySelect.value = 'best';
        }
      } else {
        qualityGroup.style.display = 'block';
        // Apply default video quality from settings
        const qualitySelect = document.getElementById('quality-select');
        const settings = this.settingsManager.settings;
        if (qualitySelect && settings?.default_video_quality) {
          qualitySelect.value = settings.default_video_quality;
        }
      }
    }

    // Update form grid layout when quality is hidden
    const formGrid = qualityGroup?.closest('.form-grid');
    if (formGrid) {
      if (selectedType === 'audio') {
        formGrid.classList.remove('form-grid-cols-3');
        formGrid.classList.add('form-grid-cols-2');
      } else {
        formGrid.classList.remove('form-grid-cols-2');
        formGrid.classList.add('form-grid-cols-3');
      }
    }
  }

  handleKeyboardShortcuts(event) {
    // Ctrl/Cmd + K to focus search
    if ((event.ctrlKey || event.metaKey) && event.key === 'k') {
      event.preventDefault();
      const urlInput = document.getElementById('url-input');
      if (urlInput) {
        urlInput.focus();
        urlInput.select();
      }
    }

    // Escape to close modals/panels
    if (event.key === 'Escape') {
      this.settingsManager.closePanel();
    }

    // Ctrl/Cmd + , to open settings
    if ((event.ctrlKey || event.metaKey) && event.key === ',') {
      event.preventDefault();
      this.settingsManager.togglePanel();
    }
  }

  handleVisibilityChange() {
    if (document.hidden) {
      this.stopPolling();
    } else {
      this.startPolling();
    }
  }

  startPolling() {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
    }

    this.pollInterval = setInterval(async () => {
      try {
        await this.downloadManager.refreshDownloads();
      } catch (error) {
        console.error('Polling failed:', error);
      }
    }, 2000); // More frequent updates for better progress visibility
  }

  stopPolling() {
    if (this.pollInterval) {
      clearInterval(this.pollInterval);
      this.pollInterval = null;
    }
  }

  async loadInitialData() {
    try {
      // Load downloads
      await this.downloadManager.refreshDownloads();
      
      // Load settings first
      await this.settingsManager.loadSettings();
      
      // Apply default settings to form
      this.applySettingsToForm();
      
      // Check for updates
      await this.settingsManager.checkForUpdates();
      
    } catch (error) {
      console.error('Failed to load initial data:', error);
    }
  }

  applySettingsToForm() {
    // Apply default settings to the download form
    const settings = this.settingsManager.settings;
    if (!settings) return;

    // Set default video format
    const formatSelect = document.getElementById('format-select');
    if (formatSelect && settings.default_video_format) {
      formatSelect.value = settings.default_video_format;
    }

    // Set default video quality
    const qualitySelect = document.getElementById('quality-select');
    if (qualitySelect && settings.default_video_quality) {
      qualitySelect.value = settings.default_video_quality;
    }
  }

  cleanup() {
    this.stopPolling();
    this.downloadManager.cleanup();
    this.settingsManager.cleanup();
    this.uiManager.cleanup();
  }

  // Public API methods
  async refreshAll() {
    await this.loadInitialData();
  }

  getStatus() {
    return {
      initialized: this.isInitialized,
      polling: !!this.pollInterval,
      downloadsCount: this.downloadManager.getDownloadsCount()
    };
  }
}

// Initialize application when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initApp);
} else {
  initApp();
}

function initApp() {
  window.goytApp = new goytApp();
  window.goytApp.init().catch(error => {
    console.error('Failed to initialize goyt:', error);
  });
}

// Export for module usage
export { goytApp };