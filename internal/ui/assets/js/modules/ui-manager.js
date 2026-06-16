/**
 * UI Manager Module
 * Handles all UI interactions and DOM manipulation
 */

import { icon } from './icons.js';

export class UIManager {
  constructor(apiClient = null) {
    this.apiClient = apiClient;
    this.notifications = [];
    this.accordionStates = {
      completed: true,
      downloading: true,
      queued: true,
      processing: true,
      failed: true
    };
  }

  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text ?? '';
    return div.innerHTML;
  }

  init() {
    this.createNotificationContainer();
    this.setupAccordions();
    this.setupFormBehavior();
    this.updateConnectionStatus(true);
  }

  setupFormBehavior() {
    // Handle type selector change to update format options
    const typeSelect = document.querySelector('#type-select');
    if (typeSelect) {
      typeSelect.addEventListener('change', (e) => {
        this.updateFormatOptions(e.target.value);
      });
    }
  }

  // Notification system
  createNotificationContainer() {
    if (document.getElementById('notification-container')) return;

    const container = document.createElement('div');
    container.id = 'notification-container';
    container.style.cssText = `
      position: fixed;
      top: 20px;
      right: 20px;
      z-index: 1080;
      display: flex;
      flex-direction: column;
      gap: 10px;
      pointer-events: none;
    `;
    document.body.appendChild(container);
  }

  showNotification(message, type = 'info', duration = null) {
    // Errors linger so failures stay readable; other toasts clear sooner.
    if (duration === null) {
      duration = type === 'error' ? 5000 : 3500;
    }

    const notification = document.createElement('div');
    notification.className = `card notification notification-${type}`;

    notification.innerHTML = `
      <div class="flex items-center gap-2">
        ${icon(this.getNotificationIcon(type), 'icon-sm')}
        <span>${this.escapeHtml(message)}</span>
        <button type="button" class="btn-close ml-auto" aria-label="Close">
          ${icon('x', 'icon-sm')}
        </button>
      </div>
    `;

    // Add close functionality
    const closeBtn = notification.querySelector('.btn-close');
    closeBtn.addEventListener('click', () => this.removeNotification(notification));

    // Auto remove after duration
    if (duration > 0) {
      setTimeout(() => this.removeNotification(notification), duration);
    }

    const container = document.getElementById('notification-container');
    container.appendChild(notification);

    this.notifications.push(notification);
  }

  removeNotification(notification) {
    notification.classList.add('leaving');
    setTimeout(() => {
      if (notification.parentNode) {
        notification.parentNode.removeChild(notification);
      }
      this.notifications = this.notifications.filter(n => n !== notification);
    }, 300);
  }

  getNotificationIcon(type) {
    const icons = {
      success: 'check-circle',
      error: 'alert-circle',
      warning: 'alert-triangle',
      info: 'info'
    };
    return icons[type] || icons.info;
  }

  // Connection status
  updateConnectionStatus(isConnected) {
    const statusElement = document.getElementById('connection-status');
    if (!statusElement) return;

    statusElement.className = `badge ${isConnected ? 'badge-success' : 'badge-danger'}`;
    statusElement.innerHTML = `
      ${icon(isConnected ? 'wifi' : 'wifi-off', 'icon-sm')}
      ${isConnected ? 'Connected' : 'Disconnected'}
    `;
  }

  // URL validation UI
  showUrlValidating(message = 'Validating URL...') {
    const container = document.getElementById('url-validation');
    if (!container) return;

    container.innerHTML = `
      <div class="flex items-center gap-2 text-secondary">
        <div class="loading-spinner"></div>
        <span class="text-sm">${this.escapeHtml(message)}</span>
      </div>
    `;
    container.classList.remove('hidden');
  }

  showUrlValidationResult(result) {
    const container = document.getElementById('url-validation');
    if (!container) return;

    if (result.valid) {
      container.innerHTML = `
        <div class="flex items-center gap-2 text-success">
          ${icon('check-circle', 'icon-sm')}
          <span class="text-sm">Valid URL${result.is_playlist ? ` (Playlist detected)` : ''}</span>
        </div>
      `;
      
      if (result.is_playlist) {
        this.showPlaylistOptions(result);
      }
    } else {
      // The info probe could not confirm the URL, but that does not mean it is
      // unsupported: it may need cookies, be region-locked, rate-limited, or
      // temporarily unavailable. Present this as advice, not a hard error, and
      // leave the download enabled.
      container.innerHTML = `
        <div class="flex items-center gap-2" style="color: var(--warn)">
          ${icon('alert-triangle', 'icon-sm')}
          <span class="text-sm">Couldn't verify URL, but you can still try download.</span>
        </div>
      `;
    }
    container.classList.remove('hidden');
  }

  showUrlValidationWarning(message) {
    const container = document.getElementById('url-validation');
    if (!container) return;
    container.innerHTML = `
      <div class="flex items-center gap-2" style="color: var(--warn)">
        ${icon('alert-triangle', 'icon-sm')}
        <span class="text-sm">${this.escapeHtml(message)}</span>
      </div>
    `;
    container.classList.remove('hidden');
  }

  clearUrlValidation() {
    const container = document.getElementById('url-validation');
    if (container) {
      container.classList.add('hidden');
      container.innerHTML = '';
    }
    
    const playlistContainer = document.getElementById('playlist-options');
    if (playlistContainer) {
      playlistContainer.classList.add('hidden');
    }
  }

  showPlaylistOptions(playlistInfo) {
    const container = document.getElementById('playlist-options');
    if (!container) return;

    container.innerHTML = `
      <div class="card" style="margin-bottom:24px">
        <div class="card-body">
          <h5 class="card-title" style="margin-bottom:12px">Playlist detected (${playlistInfo.playlist_count || 'multiple'} videos)</h5>
          <div class="flex gap-2 flex-wrap items-center">
            <button type="button" class="btn btn-primary btn-sm" id="download-playlist">
              ${icon('list', 'icon-sm')}
              <span class="btn-text">All ${playlistInfo.playlist_count ? playlistInfo.playlist_count + ' ' : ''}videos</span>
            </button>
            <button type="button" class="btn btn-secondary btn-sm" id="download-first-video">
              ${icon('play', 'icon-sm')}
              <span class="btn-text">First only</span>
            </button>
          </div>
          ${playlistInfo.first_video_title ? `<div class="text-xs text-muted mt-1 truncate" title="${this.escapeHtml(playlistInfo.first_video_title)}">${this.escapeHtml(playlistInfo.first_video_title)}</div>` : ''}
        </div>
      </div>
    `;
    container.classList.remove('hidden');

    // Add event listeners
    document.getElementById('download-playlist')?.addEventListener('click', () => {
      this.triggerPlaylistDownload('playlist');
    });
    
    document.getElementById('download-first-video')?.addEventListener('click', () => {
      this.triggerPlaylistDownload('first');
    });
  }

  triggerPlaylistDownload(type) {
    // Immediately disable buttons and show loading state
    const playlistButton = document.getElementById('download-playlist');
    const firstVideoButton = document.getElementById('download-first-video');
    
    if (type === 'playlist' && playlistButton) {
      this.setLoading('download-playlist', true, 'Processing Playlist...');
    } else if (type === 'first' && firstVideoButton) {
      this.setLoading('download-first-video', true, 'Starting Download...');
    }
    
    // Disable both buttons to prevent multiple clicks
    if (playlistButton) playlistButton.disabled = true;
    if (firstVideoButton) firstVideoButton.disabled = true;
    
    const event = new CustomEvent('playlistDownload', {
      detail: { type }
    });
    document.dispatchEvent(event);
  }

  // Accordion functionality
  setupAccordions() {
    document.addEventListener('click', (e) => {
      const header = e.target.closest('.section-header');
      if (header?.dataset.section) {
        this.toggleAccordion(header.dataset.section);
      }
    });
    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const header = e.target.closest?.('.section-header');
      if (header?.dataset.section) {
        e.preventDefault();
        this.toggleAccordion(header.dataset.section);
      }
    });
  }

  toggleAccordion(sectionId) {
    const content = document.getElementById(`${sectionId}-content`);
    const header = document.querySelector(`[data-section="${sectionId}"]`);

    if (!content) return;

    const isExpanded = this.accordionStates[sectionId];
    this.accordionStates[sectionId] = !isExpanded;

    content.classList.toggle('section-collapsed', isExpanded);
    content.classList.toggle('section-expanded', !isExpanded);
    header?.setAttribute('aria-expanded', String(!isExpanded));
  }

  // Progress bars
  updateProgressBar(element, percentage, status = '') {
    if (!element) return;

    const progressBar = element.querySelector('.progress-bar');
    if (progressBar) {
      progressBar.style.width = `${Math.max(0, Math.min(100, percentage))}%`;
      
      // Update progress bar color based on status
      progressBar.className = 'progress-bar';
      if (status === 'completed') {
        progressBar.classList.add('progress-bar-success');
      } else if (status === 'failed' || status === 'error') {
        progressBar.classList.add('progress-bar-danger');
      } else if (status === 'post-processing') {
        progressBar.classList.add('progress-bar-warning');
      }
    }
  }

  // Form helpers
  getFormData(formId) {
    const form = document.getElementById(formId);
    if (!form) return null;

    const formData = new FormData(form);
    const data = {};
    
    for (const [key, value] of formData.entries()) {
      data[key] = value;
    }
    
    return data;
  }

  setFormData(formId, data) {
    const form = document.getElementById(formId);
    if (!form) return;

    Object.entries(data).forEach(([key, value]) => {
      const element = form.elements[key];
      if (element) {
        if (element.type === 'checkbox') {
          element.checked = Boolean(value);
        } else {
          element.value = value;
        }
      }
    });
  }

  async resetForm(formId) {
    const form = document.getElementById(formId);
    if (!form) return;

    // Clear the URL field and validation
    const urlInput = form.querySelector('#url-input');
    if (urlInput) {
      urlInput.value = '';
    }
    this.clearUrlValidation();

    // Reset to config defaults if API client is available
    if (this.apiClient) {
      try {
        const config = await this.apiClient.getConfig();
        
        // Set default values from config
        const typeSelect = form.querySelector('#type-select');
        const qualitySelect = form.querySelector('#quality-select');
        const formatSelect = form.querySelector('#format-select');

        if (typeSelect) {
          typeSelect.value = 'video'; // Always default to video
        }

        if (qualitySelect) {
          qualitySelect.value = config.default_video_quality || '1080p';
        }

        if (formatSelect) {
          // Update format options first based on type
          this.updateFormatOptions('video');
          formatSelect.value = config.default_video_format || 'mp4';
        }
        
      } catch (error) {
        console.warn('Failed to get config for form reset, using fallback defaults:', error);
        // Fallback to hardcoded defaults
        const typeSelect = form.querySelector('#type-select');
        const qualitySelect = form.querySelector('#quality-select');
        const formatSelect = form.querySelector('#format-select');

        if (typeSelect) typeSelect.value = 'video';
        if (qualitySelect) qualitySelect.value = '1080p';
        if (formatSelect) formatSelect.value = 'mp4';
      }
    } else {
      // No API client, just reset to first options
      form.reset();
    }
  }

  updateFormatOptions(type) {
    const formatSelect = document.querySelector('#format-select');
    if (!formatSelect) return;

    // Clear existing options
    formatSelect.innerHTML = '';

    if (type === 'audio') {
      // Audio format options
      const audioFormats = [
        { value: 'mp3', text: 'MP3' },
        { value: 'aac', text: 'AAC' },
        { value: 'flac', text: 'FLAC' },
        { value: 'wav', text: 'WAV' },
        { value: 'ogg', text: 'OGG' }
      ];
      
      audioFormats.forEach(format => {
        const option = document.createElement('option');
        option.value = format.value;
        option.textContent = format.text;
        formatSelect.appendChild(option);
      });
      
      // Set default to mp3 for audio
      formatSelect.value = 'mp3';
    } else {
      // Video format options
      const videoFormats = [
        { value: 'mp4', text: 'MP4' },
        { value: 'mkv', text: 'MKV' },
        { value: 'webm', text: 'WebM' },
        { value: 'avi', text: 'AVI' }
      ];
      
      videoFormats.forEach(format => {
        const option = document.createElement('option');
        option.value = format.value;
        option.textContent = format.text;
        formatSelect.appendChild(option);
      });
      
      // Set default to mp4 for video
      formatSelect.value = 'mp4';
    }
  }

  // Loading states
  setLoading(elementId, isLoading, loadingText = 'Loading...') {
    const element = document.getElementById(elementId);
    if (!element) return;

    if (isLoading) {
      element.disabled = true;
      const originalHTML = element.innerHTML;
      element.dataset.originalHTML = originalHTML;
      element.innerHTML = `
        <div class="loading-spinner"></div>
        <span>${loadingText}</span>
      `;
    } else {
      element.disabled = false;
      element.innerHTML = element.dataset.originalHTML || element.innerHTML;
      delete element.dataset.originalHTML;
    }
  }

  // Modal helpers (for future use)
  showModal(modalId) {
    const modal = document.getElementById(modalId);
    if (modal) {
      modal.classList.add('show');
      document.body.classList.add('modal-open');
    }
  }

  hideModal(modalId) {
    const modal = document.getElementById(modalId);
    if (modal) {
      modal.classList.remove('show');
      document.body.classList.remove('modal-open');
    }
  }

  // Utility methods
  formatFileSize(bytes) {
    if (bytes === 0) return '0 Bytes';
    
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  formatDuration(seconds) {
    if (!seconds || seconds < 0) return '00:00';
    
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const secs = Math.floor(seconds % 60);
    
    if (hours > 0) {
      return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
    }
    
    return `${minutes.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
  }

  formatDate(dateString) {
    if (!dateString) return 'Unknown';
    
    const date = new Date(dateString);
    return date.toLocaleString();
  }

  // Cleanup
  cleanup() {
    // Remove all notifications
    this.notifications.forEach(notification => {
      this.removeNotification(notification);
    });
    
    // Clear any timers or intervals if needed
    // This can be extended as needed
  }
}
