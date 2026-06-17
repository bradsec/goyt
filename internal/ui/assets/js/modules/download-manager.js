/**
 * Download Manager Module
 * Handles download operations and UI updates
 */

import { icon } from './icons.js';

export class DownloadManager {
  constructor(apiClient, uiManager) {
    this.apiClient = apiClient;
    this.uiManager = uiManager;
    this.downloads = [];
    this.lastUpdateHash = new Map(); // Track last update hash for each download
    this.renderedSections = {}; // Per-section render state for keyed DOM patching
    this.sections = {
      queued: { downloads: [], expanded: true },
      downloading: { downloads: [], expanded: true },
      processing: { downloads: [], expanded: true },
      completed: { downloads: [], expanded: true },
      failed: { downloads: [], expanded: true }
    };
  }

  async init() {
    this.setupEventListeners();
    await this.refreshDownloads();
  }

  setupEventListeners() {
    // Playlist download events
    document.addEventListener('playlistDownload', this.handlePlaylistDownload.bind(this));
    
    // Download action buttons
    document.addEventListener('click', (e) => {
      // Find the button element, even if clicked on nested elements (span, icon, etc.)
      const buttonElement = e.target.closest('[data-action][data-download-id]');
      
      if (buttonElement) {
        const action = buttonElement.dataset.action;
        const downloadId = buttonElement.dataset.downloadId;
        
        if (action && downloadId) {
          this.handleDownloadAction(action, downloadId, buttonElement);
        }
      }
    });

    // Bulk action buttons
    const bulkActions = {
      'clear-queued': () => this.clearQueuedDownloads(),
      'delete-completed': () => this.deleteCompletedDownloads(),
      'clear-failed': () => this.clearFailedDownloads()
    };

    Object.entries(bulkActions).forEach(([action, handler]) => {
      const button = document.getElementById(action);
      if (button) {
        button.addEventListener('click', handler);
      }
    });
  }

  async startDownload(downloadData) {
    try {
      this.uiManager.setLoading('submit-button', true, 'Starting...');
      
      const result = await this.apiClient.startDownload(downloadData);
      
      if (result.success || result.id) {
        this.uiManager.showNotification('Download started successfully', 'success');
        await this.refreshDownloads();
      } else {
        throw new Error(result.error || 'Unknown error');
      }
    } catch (error) {
      if (error.code === 'ALREADY_DOWNLOADED' || error.code === 'ALREADY_PROCESSING') {
        this.uiManager.showNotification(error.message, 'info');
      } else {
        this.uiManager.showNotification(`Failed to start download: ${error.message}`, 'error');
      }
      throw error;
    } finally {
      this.uiManager.setLoading('submit-button', false);
    }
  }

  async handlePlaylistDownload(event) {
    const { type } = event.detail;
    const formData = this.uiManager.getFormData('download-form');
    
    if (!formData || !formData.url) {
      this.uiManager.showNotification('Please enter a valid URL', 'warning');
      this.resetPlaylistButtons();
      return;
    }

    try {
      const downloadData = {
        url: formData.url,
        type: formData.type || 'video',
        quality: formData.quality || 'best',
        format: formData.format || 'mp4'
      };

      // The server enumerates the playlist before the download is added, which
      // can take a while on large playlists or slow networks. Give immediate
      // feedback so the UI does not look idle during that background work.
      if (type === 'playlist') {
        this.uiManager.showNotification('Fetching playlist info, this can take a while for large playlists...', 'info');
        await this.apiClient.startPlaylistDownload(
          downloadData.url,
          downloadData.type,
          downloadData.quality,
          downloadData.format
        );
        this.uiManager.showNotification('Playlist download started', 'success');
      } else if (type === 'first') {
        this.uiManager.showNotification('Fetching first video...', 'info');
        await this.apiClient.downloadFirstVideo(
          downloadData.url,
          downloadData.type,
          downloadData.quality,
          downloadData.format
        );
        this.uiManager.showNotification('First video download started', 'success');
      }

      await this.uiManager.resetForm('download-form');
      this.uiManager.clearUrlValidation(); // Hide playlist options after successful start
      this.refreshDownloads(); // Non-blocking refresh
    } catch (error) {
      this.uiManager.showNotification(`Failed to start download: ${error.message}`, 'error');
    } finally {
      this.resetPlaylistButtons();
    }
  }

  resetPlaylistButtons() {
    // Reset both playlist buttons to their original state
    this.uiManager.setLoading('download-playlist', false);
    this.uiManager.setLoading('download-first-video', false);
    
    const playlistButton = document.getElementById('download-playlist');
    const firstVideoButton = document.getElementById('download-first-video');
    
    if (playlistButton) playlistButton.disabled = false;
    if (firstVideoButton) firstVideoButton.disabled = false;
  }

  async handleDownloadAction(action, downloadId, buttonElement) {
    if (action === 'play') {
      this.openMediaPlayer(downloadId);
      return;
    }

    const originalInnerHTML = buttonElement.innerHTML;
    
    try {
      buttonElement.disabled = true;
      buttonElement.innerHTML = '<div class="loading-spinner"></div>';

      switch (action) {
        case 'cancel':
          await this.apiClient.cancelDownload(downloadId);
          this.uiManager.showNotification('Download cancelled', 'info');
          // Refresh downloads after state-changing actions
          this.refreshDownloads(); // Non-blocking refresh
          break;
        case 'pause':
          await this.apiClient.pauseDownload(downloadId);
          this.uiManager.showNotification('Download paused', 'info');
          this.refreshDownloads(); // Non-blocking refresh
          break;
        case 'resume':
          await this.apiClient.resumeDownload(downloadId);
          this.uiManager.showNotification('Download resumed', 'info');
          this.refreshDownloads(); // Non-blocking refresh
          break;
        case 'retry':
          await this.apiClient.retryDownload(downloadId);
          this.uiManager.showNotification('Download retried', 'info');
          this.refreshDownloads(); // Non-blocking refresh
          break;
        case 'remove':
          await this.apiClient.removeDownload(downloadId);
          this.uiManager.showNotification('Download removed', 'info');
          // For remove action, immediately hide the item for better UX
          const itemElement = buttonElement.closest('.download-item');
          if (itemElement) {
            itemElement.style.opacity = '0.5';
            itemElement.style.pointerEvents = 'none';
          }
          this.refreshDownloads(); // Non-blocking refresh
          return; // Don't wait for refresh
        case 'download':
          await this.apiClient.downloadFile(downloadId);
          this.uiManager.showNotification('File download started', 'success');
          return; // Don't refresh downloads for file download
        case 'convert':
          await this.apiClient.convertDownload(downloadId);
          this.uiManager.showNotification('Converting to H.264 + AAC', 'info');
          this.refreshDownloads(); // Non-blocking refresh
          break;
        default:
          throw new Error('Unknown action');
      }
    } catch (error) {
      this.uiManager.showNotification(`Action failed: ${error.message}`, 'error');
    } finally {
      buttonElement.disabled = false;
      buttonElement.innerHTML = originalInnerHTML;
    }
  }

  async refreshDownloads() {
    try {
      const downloads = await this.apiClient.getDownloads();
      const newDownloads = downloads || [];
      
      // Quick check if data has changed at all
      if (this.hasDownloadsChanged(newDownloads)) {
        this.downloads = newDownloads;
        this.categorizeDownloads();
        this.renderDownloads();
      }
    } catch (error) {
      console.error('Failed to refresh downloads:', error);
      this.uiManager.updateConnectionStatus(false);
    }
  }

  // Check if the downloads data has changed
  hasDownloadsChanged(newDownloads) {
    if (this.downloads.length !== newDownloads.length) {
      return true;
    }
    
    // Quick hash comparison of all downloads
    const oldHash = this.downloads.map(d => this.createDownloadHash(d)).sort().join('|');
    const newHash = newDownloads.map(d => this.createDownloadHash(d)).sort().join('|');
    
    return oldHash !== newHash;
  }

  categorizeDownloads() {
    // Reset sections
    Object.keys(this.sections).forEach(key => {
      this.sections[key].downloads = [];
    });

    // Categorize downloads
    this.downloads.forEach(download => {
      switch (download.status) {
        case 'queued':
          this.sections.queued.downloads.push(download);
          break;
        case 'downloading':
          this.sections.downloading.downloads.push(download);
          break;
        case 'post-processing':
        case 'processing':
        case 'converting':
          this.sections.processing.downloads.push(download);
          break;
        case 'completed':
        case 'already_exists':
          this.sections.completed.downloads.push(download);
          break;
        case 'failed':
        case 'error':
          this.sections.failed.downloads.push(download);
          break;
      }
    });

    // Sort downloads within each section
    Object.keys(this.sections).forEach(key => {
      this.sections[key].downloads.sort(this.getSortFunction(key));
    });
  }

  // Create a hash for a download to detect changes
  createDownloadHash(download) {
    if (!download) return '';
    // Include key fields but round percentage to avoid minor floating point differences
    const progressPhase = download.progress?.phase || '';
    const progressFrame = download.progress?.current_frame || 0;
    const progressETA = download.progress?.eta || '';
    const progressSpeed = download.progress?.speed || '';
    // Round percentage to 1 decimal place to allow gradual updates while avoiding micro-changes
    const roundedPercentage = Math.round((download.progress?.percentage || 0) * 10) / 10;
    // file_size and codecs land asynchronously after a download completes (stat
    // + probe), so they must be in the hash or the card never repaints to show
    // the final size / codec badge / convert action.
    const meta = `${download.file_size || ''}_${download.video_codec || ''}_${download.audio_codec || ''}_${download.converted ? 1 : 0}`;
    const key = `${download.id}_${download.status}_${roundedPercentage}_${progressPhase}_${progressFrame}_${progressETA}_${progressSpeed}_${download.title}_${download.error || ''}_${meta}`;
    return key;
  }

  getSortFunction(sectionKey) {
    return (a, b) => {
      let aDate, bDate;
      
      switch (sectionKey) {
        case 'queued':
          aDate = new Date(a.created_at);
          bDate = new Date(b.created_at);
          // Primary sort: oldest first, secondary sort: by ID for stability
          const queueResult = aDate - bDate;
          return queueResult !== 0 ? queueResult : a.id.localeCompare(b.id);
        case 'downloading':
        case 'processing':
          aDate = new Date(a.started_at || a.created_at);
          bDate = new Date(b.started_at || b.created_at);
          // Primary sort: newest first, secondary sort: by ID for stability
          const activeResult = bDate - aDate;
          return activeResult !== 0 ? activeResult : a.id.localeCompare(b.id);
        case 'completed':
          aDate = new Date(a.completed_at || a.created_at);
          bDate = new Date(b.completed_at || b.created_at);
          // Primary sort: newest first, secondary sort: by ID for stability
          const completedResult = bDate - aDate;
          return completedResult !== 0 ? completedResult : a.id.localeCompare(b.id);
        case 'failed':
          aDate = new Date(a.error_at || a.created_at);
          bDate = new Date(b.error_at || b.created_at);
          // Primary sort: newest first, secondary sort: by ID for stability
          const failedResult = bDate - aDate;
          return failedResult !== 0 ? failedResult : a.id.localeCompare(b.id);
        default:
          // Fallback to ID-based sorting for any unknown sections
          return a.id.localeCompare(b.id);
      }
    };
  }

  renderDownloads() {
    Object.entries(this.sections).forEach(([sectionKey, section]) => {
      this.renderSection(sectionKey, section);
    });

    const emptyBoard = document.getElementById('empty-board');
    if (emptyBoard) {
      const total = Object.values(this.sections)
        .reduce((sum, section) => sum + section.downloads.length, 0);
      emptyBoard.style.display = total === 0 ? 'flex' : 'none';
    }

    // Update connection status
    this.uiManager.updateConnectionStatus(true);
  }

  renderSection(sectionKey, section) {
    const container = document.getElementById(`${sectionKey}-content`);
    if (!container) return;

    const downloads = section.downloads;
    const count = downloads.length;

    // Always update count and visibility as these are lightweight
    const countElement = document.getElementById(`${sectionKey}-count`);
    if (countElement) {
      countElement.textContent = count.toString();
    }

    const sectionElement = document.getElementById(`${sectionKey}-section`);
    if (sectionElement) {
      sectionElement.style.display = count > 0 ? 'block' : 'none';
    }

    this.renderedSections = this.renderedSections || {};
    const prev = this.renderedSections[sectionKey];

    if (count === 0) {
      if (prev !== null) {
        container.innerHTML = '<p class="text-center text-muted">No downloads</p>';
        this.renderedSections[sectionKey] = null;
      }
      return;
    }

    const items = downloads.map((download, index) => ({
      id: download.id,
      status: download.status,
      hash: this.createDownloadHash(download),
      download,
      index
    }));
    const orderKey = items.map(it => it.id).join('|');
    const list = container.querySelector('.download-list');

    // Rebuild fully only when the set or order of items changes. Otherwise patch
    // in place so cards that did not change keep their running CSS animations,
    // and the active card's progress bar keeps its smooth width transition
    // instead of being recreated (and reset) on every 2s poll.
    if (!list || !prev || prev.orderKey !== orderKey) {
      const html = items.map(it => this.renderDownloadItem(it.download, sectionKey, it.index)).join('');
      container.innerHTML = `<div class="download-list">${html}</div>`;
      this.renderedSections[sectionKey] = { orderKey, items };
      return;
    }

    const prevById = new Map(prev.items.map(it => [it.id, it]));
    items.forEach((it) => {
      const before = prevById.get(it.id);
      if (before && before.hash === it.hash) return; // unchanged, leave it alone

      const el = list.children[it.index];
      if (!el) return;

      // Active download: patch the mutable fields without replacing the node so
      // the progress bar keeps its smooth width transition across polls. Other
      // states (processing/converting) carry detail blocks that need a full
      // re-render, so they fall through to node replacement.
      if (before && before.status === it.status && it.status === 'downloading'
          && this.updateItemInPlace(el, it.download, sectionKey, it.index)) {
        return;
      }

      // Status changed (different badge/actions/progress): replace just this card.
      const tmp = document.createElement('div');
      tmp.innerHTML = this.renderDownloadItem(it.download, sectionKey, it.index);
      const fresh = tmp.firstElementChild;
      if (fresh) list.replaceChild(fresh, el);
    });

    this.renderedSections[sectionKey] = { orderKey, items };
  }

  // Patches the live, changing parts of an existing card without recreating its
  // DOM, preserving the progress bar's width transition and the indeterminate
  // slide animation. Returns false if the card has no progress block to patch
  // (terminal states), so the caller falls back to a full replace.
  updateItemInPlace(el, download, sectionKey, index) {
    const progressEl = el.querySelector('.download-progress');
    if (!progressEl) return false;

    const percentage = this.parsePercentage(download.progress?.percentage);
    const bounded = Math.max(0, Math.min(100, percentage));
    const stage = this.getStageInfo(download, bounded, sectionKey, index);

    const labelEl = progressEl.querySelector('.stage-label');
    if (labelEl) labelEl.textContent = stage.label;

    const statsEl = progressEl.querySelector('.stage-stats');
    if (statsEl) statsEl.innerHTML = this.buildProgressStats(download, bounded, stage);

    const containerEl = progressEl.querySelector('.progress-container');
    if (containerEl) containerEl.classList.toggle('progress-indeterminate', !!stage.indeterminate);

    const barEl = progressEl.querySelector('.progress-bar');
    if (barEl) barEl.style.width = `${stage.indeterminate ? 30 : bounded}%`;

    return true;
  }

  renderDownloadItem(download, sectionKey, index = 0) {
    const progress = download.progress || {};
    const percentage = this.parsePercentage(progress.percentage);

    return `
      <div class="download-item" data-download-id="${download.id}" data-item-key="${download.id}-${sectionKey}">
        <div class="download-title">${this.escapeHtml(download.title || 'Unknown Title')}</div>
        <div class="download-url">${this.escapeHtml(download.url)}</div>
        
        <div class="download-meta">
          <div class="flex items-center gap-2">
            ${this.renderStatusBadge(download.status)}
            <span class="text-sm text-secondary">${this.escapeHtml(download.type)}</span>
            <span class="text-sm text-secondary">${this.escapeHtml(download.quality)}</span>
            <span class="text-sm text-secondary">${this.escapeHtml(download.format)}</span>
            ${this.renderCodecBadge(download)}
            ${this.renderFileSize(download)}
          </div>
          <div class="text-sm text-secondary">
            ${this.uiManager.formatDate(this.getRelevantDate(download, sectionKey))}
          </div>
        </div>

        ${download.error ? `<div class="download-error">${this.escapeHtml(download.error)}</div>` : ''}
        ${this.renderProgress(download, percentage, sectionKey, index)}
        ${this.renderDownloadActions(download, sectionKey)}
      </div>
    `;
  }

  // Shows the final file size on completed entries, e.g. "1.2 GB".
  renderFileSize(download) {
    const terminal = ['completed', 'already_exists'];
    if (!terminal.includes(download.status) || !download.file_size) return '';
    return `<span class="text-sm text-secondary">[${this.formatBytes(download.file_size)}]</span>`;
  }

  formatBytes(bytes) {
    const b = Number(bytes);
    if (!b || b < 0) return '';
    const unit = 1024;
    if (b < unit) return `${b} B`;
    const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
    let exp = Math.floor(Math.log(b) / Math.log(unit));
    if (exp > units.length) exp = units.length;
    return `${(b / Math.pow(unit, exp)).toFixed(1)} ${units[exp - 1]}`;
  }

  renderStatusBadge(status) {
    const statusConfig = {
      queued: { class: 'badge-secondary', icon: 'clock', text: 'Queued' },
      downloading: { class: 'badge-primary', icon: 'download', text: 'Downloading' },
      'post-processing': { class: 'badge-warning', icon: 'activity', text: 'Processing' },
      converting: { class: 'badge-warning', icon: 'activity', text: 'Converting' },
      completed: { class: 'badge-success', icon: 'check-circle', text: 'Completed' },
      already_exists: { class: 'badge-success', icon: 'check-circle', text: 'Already Exists' },
      failed: { class: 'badge-danger', icon: 'alert-circle', text: 'Failed' },
      error: { class: 'badge-danger', icon: 'alert-circle', text: 'Error' },
      paused: { class: 'badge-secondary', icon: 'pause', text: 'Paused' },
      canceled: { class: 'badge-secondary', icon: 'x-circle', text: 'Canceled' },
      cancelled: { class: 'badge-secondary', icon: 'x-circle', text: 'Cancelled' }
    };

    const config = statusConfig[status] || statusConfig.queued;
    return `
      <span class="badge ${config.class}">
        ${icon(config.icon, 'icon-xs')}
        ${config.text}
      </span>
    `;
  }

  // Shows the file's actual codecs once probed, e.g. "H.264 · AAC" or
  // "VP9 · Opus". Absent for downloads completed before codec probing existed.
  renderCodecBadge(download) {
    const v = download.video_codec;
    const a = download.audio_codec;
    if (!v && !a) return '';
    const pretty = (c) => {
      if (!c) return '';
      const map = { h264: 'H.264', hevc: 'HEVC', aac: 'AAC', vp9: 'VP9', vp8: 'VP8', av1: 'AV1', opus: 'Opus', vorbis: 'Vorbis' };
      return map[c.toLowerCase()] || c.toUpperCase();
    };
    const parts = [pretty(v), pretty(a)].filter(Boolean).join(' ');
    return `<span class="text-sm text-secondary">${this.escapeHtml(parts)}</span>`;
  }

  renderProgress(download, percentage, sectionKey, index = 0) {
    const terminal = ['completed', 'already_exists', 'failed', 'error', 'cancelled', 'canceled'];
    if (terminal.includes(download.status)) {
      return '';
    }

    const progress = download.progress || {};
    const boundedPercentage = Math.max(0, Math.min(100, percentage));
    const stage = this.getStageInfo(download, boundedPercentage, sectionKey, index);

    let ffmpegProgressHtml = '';
    if (progress.phase || progress.ffmpeg_progress || progress.video_codec || progress.current_frame > 0) {
      ffmpegProgressHtml = this.renderFFMPEGProgress(progress);
    }

    const barWidth = stage.indeterminate ? 30 : boundedPercentage;

    return `
      <div class="download-progress">
        <div class="download-stage">
          <span class="stage-label">${stage.label}</span>
          <span class="stage-stats">${this.buildProgressStats(download, boundedPercentage, stage)}</span>
        </div>
        <div class="progress-container ${stage.indeterminate ? 'progress-indeterminate' : ''}">
          <div class="progress-bar" style="width: ${barWidth}%"></div>
        </div>
        ${ffmpegProgressHtml}
      </div>
    `;
  }

  // Builds the right-hand stats line (percent of size, speed, eta). Shared by
  // the full render and the in-place patch so both stay identical.
  buildProgressStats(download, boundedPercentage, stage) {
    const progress = download.progress || {};
    const stats = [];
    if (stage.showPercent) {
      const sizeSuffix = (download.status === 'downloading' && progress.size)
        ? ` of ${this.escapeHtml(progress.size)}`
        : '';
      stats.push(`<span>${boundedPercentage.toFixed(1)}%${sizeSuffix}</span>`);
    }
    if (progress.speed) {
      stats.push(`<span class="flex items-center gap-1">${icon('gauge', 'icon-xs')} ${this.escapeHtml(progress.speed)}</span>`);
    }
    if (progress.eta) {
      stats.push(`<span class="flex items-center gap-1">${icon('clock', 'icon-xs')} eta ${this.escapeHtml(progress.eta)}</span>`);
    }
    return stats.join('');
  }

  // Stage drives the always-on feedback line: what the system is doing with
  // this entry right now, in plain words.
  getStageInfo(download, percentage, sectionKey, index) {
    const phase = download.progress?.phase;

    if (download.status === 'queued') {
      return { label: `Waiting, position ${index + 1} in queue`, indeterminate: true, showPercent: false };
    }
    if (download.status === 'paused') {
      return { label: `Paused at ${percentage.toFixed(0)}%`, indeterminate: false, showPercent: false };
    }
    if (download.status === 'post-processing' || download.status === 'processing' || phase === 'converting' || phase === 'processing') {
      return { label: 'Processing media', indeterminate: percentage <= 0, showPercent: percentage > 0 };
    }
    if (download.status === 'converting') {
      return { label: 'Converting to H.264 + AAC', indeterminate: percentage <= 0, showPercent: percentage > 0 };
    }
    if (download.status === 'downloading') {
      if (percentage <= 0) {
        return { label: 'Contacting site, fetching streams', indeterminate: true, showPercent: false };
      }
      return { label: 'Downloading', indeterminate: false, showPercent: true };
    }
    return { label: this.formatPhase(phase || download.status), indeterminate: percentage <= 0, showPercent: percentage > 0 };
  }

  formatPhase(phase) {
    const phaseMap = {
      'initializing': 'Initializing',
      'downloading': 'Downloading',
      'processing': 'Processing',
      'converting': 'Converting'
    };
    return phaseMap[phase] || phase;
  }

  renderFFMPEGProgress(progress) {
    if (!progress || Object.keys(progress).length === 0) return '';

    const sections = [];

    // FFMPEG Progress Section
    if (progress.ffmpeg_progress) {
      sections.push(`
        <div class="ffmpeg-progress">
          <div class="ffmpeg-progress-header">
            <div class="ffmpeg-progress-title">
              ${icon('activity', 'icon-xs')}
              Details
            </div>
          </div>
          <div class="text-sm text-muted">${this.escapeHtml(progress.ffmpeg_progress)}</div>
        </div>
      `);
    }

    // Media Information Section
    if (progress.video_codec || progress.audio_codec || progress.resolution) {
      const mediaDetails = [];
      if (progress.video_codec) mediaDetails.push({ label: 'Video Codec', value: progress.video_codec });
      if (progress.audio_codec) mediaDetails.push({ label: 'Audio Codec', value: progress.audio_codec });
      if (progress.resolution) mediaDetails.push({ label: 'Resolution', value: progress.resolution });
      if (progress.fps && progress.fps !== '') mediaDetails.push({ label: 'FPS', value: progress.fps });
      
      if (mediaDetails.length > 0) {
        const detailsHtml = mediaDetails.map(detail => `
          <div class="ffmpeg-detail-item">
            <div class="ffmpeg-detail-label">${detail.label}</div>
            <div class="ffmpeg-detail-value">${detail.value}</div>
          </div>
        `).join('');
        
        sections.push(`
          <div class="ffmpeg-progress">
            <div class="ffmpeg-progress-header">
              <div class="ffmpeg-progress-title">
                ${icon('film', 'icon-xs')}
                Media Info
              </div>
            </div>
            <div class="ffmpeg-progress-details">
              ${detailsHtml}
            </div>
          </div>
        `);
      }
    }

    // Processing Details Section
    if (progress.current_frame > 0 || progress.bitrate || progress.processing_time) {
      const processingDetails = [];
      
      if (progress.current_frame > 0) {
        if (progress.total_frames > 0) {
          processingDetails.push({ 
            label: 'Frame Progress', 
            value: `${progress.current_frame.toLocaleString()} / ${progress.total_frames.toLocaleString()}` 
          });
        } else {
          processingDetails.push({ 
            label: 'Current Frame', 
            value: progress.current_frame.toLocaleString() 
          });
        }
      }
      
      if (progress.bitrate) processingDetails.push({ label: 'Bitrate', value: progress.bitrate });
      if (progress.processing_time) processingDetails.push({ label: 'Processing Time', value: progress.processing_time });
      
      if (processingDetails.length > 0) {
        const detailsHtml = processingDetails.map(detail => `
          <div class="ffmpeg-detail-item">
            <div class="ffmpeg-detail-label">${detail.label}</div>
            <div class="ffmpeg-detail-value">${detail.value}</div>
          </div>
        `).join('');
        
        sections.push(`
          <div class="ffmpeg-progress">
            <div class="ffmpeg-progress-header">
              <div class="ffmpeg-progress-title">
                ${icon('gauge', 'icon-xs')}
                Processing Details
              </div>
            </div>
            <div class="ffmpeg-progress-details">
              ${detailsHtml}
            </div>
          </div>
        `);
      }
    }

    return sections.join('');
  }

  openMediaPlayer(downloadId) {
    const download = this.downloads.find(d => d.id === downloadId);
    if (!download) return;

    const modal = document.getElementById('media-player-modal');
    const mount = document.getElementById('media-modal-mount');
    const title = document.getElementById('media-modal-title');
    if (!modal || !mount || !title) return;

    title.textContent = download.title || download.filename || 'Media';

    const isAudio = download.type === 'audio';
    const el = document.createElement(isAudio ? 'audio' : 'video');
    el.controls = true;
    el.autoplay = true;
    el.className = isAudio ? 'media-modal-audio' : 'media-modal-video';
    el.src = `/api/downloads/${downloadId}/stream`;

    mount.replaceChildren(el);

    if (!this._mediaModalWired) {
      const close = () => this.closeMediaPlayer();
      document.getElementById('media-modal-close')?.addEventListener('click', close);
      modal.addEventListener('close', () => this.teardownMediaPlayer());
      modal.addEventListener('click', (e) => {
        if (e.target === modal) close();
      });
      this._mediaModalWired = true;
    }

    modal.showModal();
  }

  closeMediaPlayer() {
    const modal = document.getElementById('media-player-modal');
    if (modal && modal.open) modal.close();
  }

  teardownMediaPlayer() {
    const mount = document.getElementById('media-modal-mount');
    const media = mount?.querySelector('video, audio');
    if (media) {
      media.pause();
      media.removeAttribute('src');
      media.load();
    }
    if (mount) mount.replaceChildren();
  }

  renderDownloadActions(download, sectionKey) {
    const actions = this.getAvailableActions(download.status)
      .filter(action => action !== 'convert' || this.canConvert(download));
    if (actions.length === 0) return '';

    const buttonsHtml = actions.map(action => {
      const config = this.getActionConfig(action);
      return `
        <button 
          type="button" 
          class="btn ${config.class} btn-sm" 
          data-action="${action}" 
          data-download-id="${download.id}"
          title="${config.title}"
        >
          ${icon(config.icon, 'icon-sm')}
          <span class="btn-text">${config.text}</span>
        </button>
      `;
    }).join('');

    return `<div class="download-actions">${buttonsHtml}</div>`;
  }

  getAvailableActions(status) {
    const actionMap = {
      queued: ['cancel', 'remove'],
      downloading: ['pause', 'cancel'],
      'post-processing': ['cancel'],
      converting: ['cancel'],
      completed: ['play', 'download', 'convert', 'remove'],
      already_exists: ['play', 'download', 'convert', 'remove'],
      failed: ['retry', 'remove'],
      error: ['retry', 'remove'],
      cancelled: ['retry', 'remove'],
      paused: ['resume', 'cancel', 'remove']
    };

    return actionMap[status] || ['remove'];
  }

  // Convert is offered only for completed videos that are not already H.264+AAC
  // and have not been converted yet.
  canConvert(download) {
    if (download.type !== 'video') return false;
    if (download.converted) return false;
    const v = (download.video_codec || '').toLowerCase();
    const a = (download.audio_codec || '').toLowerCase();
    return !(v === 'h264' && a === 'aac');
  }

  getActionConfig(action) {
    const configs = {
      cancel: { class: 'btn-warning', icon: 'x-circle', text: 'Cancel', title: 'Cancel download' },
      pause: { class: 'btn-secondary', icon: 'pause', text: 'Pause', title: 'Pause download' },
      resume: { class: 'btn-success', icon: 'play', text: 'Resume', title: 'Resume download' },
      retry: { class: 'btn-secondary', icon: 'refresh', text: 'Retry', title: 'Retry download' },
      remove: { class: 'btn-danger', icon: 'trash', text: 'Remove', title: 'Remove from list' },
      download: { class: 'btn-success', icon: 'file-down', text: 'Download', title: 'Download file' },
      play: { class: 'btn-primary', icon: 'play', text: 'Play', title: 'Play in browser' },
      convert: { class: 'btn-secondary', icon: 'refresh', text: 'Convert', title: 'Convert to H.264 + AAC' }
    };

    return configs[action] || { class: 'btn-secondary', icon: 'info', text: 'Unknown', title: 'Unknown action' };
  }

  // Bulk operations. These act immediately without a confirm dialog; an
  // informational banner reports when there is nothing matching to act on.
  countByStatus(...statuses) {
    return this.downloads.filter(d => statuses.includes(d.status)).length;
  }

  async clearQueuedDownloads() {
    if (this.countByStatus('queued') === 0) {
      this.uiManager.showNotification('Nothing to clear', 'info');
      return;
    }
    try {
      await this.apiClient.clearQueuedDownloads();
      this.uiManager.showNotification('Queued downloads cleared', 'success');
      await this.refreshDownloads();
    } catch (error) {
      this.uiManager.showNotification(`Failed to clear queued downloads: ${error.message}`, 'error');
    }
  }

  async deleteCompletedDownloads() {
    if (this.countByStatus('completed', 'already_exists') === 0) {
      this.uiManager.showNotification('Nothing to remove', 'info');
      return;
    }
    try {
      await this.apiClient.deleteCompletedDownloads();
      this.uiManager.showNotification('Completed downloads deleted', 'success');
      await this.refreshDownloads();
    } catch (error) {
      this.uiManager.showNotification(`Failed to delete completed downloads: ${error.message}`, 'error');
    }
  }

  async clearFailedDownloads() {
    if (this.countByStatus('failed', 'error') === 0) {
      this.uiManager.showNotification('Nothing to clear', 'info');
      return;
    }
    try {
      await this.apiClient.clearFailedDownloads();
      this.uiManager.showNotification('Failed downloads cleared', 'success');
      await this.refreshDownloads();
    } catch (error) {
      this.uiManager.showNotification(`Failed to clear failed downloads: ${error.message}`, 'error');
    }
  }

  // Utility methods
  parsePercentage(percentageStr) {
    if (!percentageStr) return 0;
    const num = parseFloat(percentageStr.toString().replace('%', ''));
    return isNaN(num) ? 0 : Math.max(0, Math.min(100, num));
  }

  getRelevantDate(download, sectionKey) {
    switch (sectionKey) {
      case 'completed':
        return download.completed_at || download.created_at;
      case 'failed':
        return download.error_at || download.created_at;
      case 'downloading':
      case 'processing':
        return download.started_at || download.created_at;
      default:
        return download.created_at;
    }
  }

  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  getDownloadsCount() {
    return this.downloads.length;
  }

  // Cleanup
  cleanup() {
    // Any cleanup needed for the download manager
  }
}