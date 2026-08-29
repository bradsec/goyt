/**
 * API Client Module
 * Handles all API communication with the backend
 */

export interface URLValidationResult {
  valid: boolean;
  is_playlist?: boolean;
  playlist_count?: number;
  first_video_title?: string;
}

export class ApiClient {
  baseUrl: string;
  timeout: number;
  downloadStartTimeout: number;
  playlistTimeout: number;

  constructor() {
    this.baseUrl = '';
    this.timeout = 30000; // 30 seconds
    // Per-action timeouts (ms). These sit above the matching server-side
    // timeouts so the server finishes (and returns a real error) before the
    // client aborts. setActionTimeouts() refreshes them from the saved config.
    this.downloadStartTimeout = 90000;  // server download_start (60s) + headroom
    this.playlistTimeout = 210000;      // server playlist_load (180s) + headroom
  }

  // setActionTimeouts derives the client fetch timeouts from the configured
  // server timeouts (seconds), adding headroom so the server aborts first.
  setActionTimeouts({ downloadStartSeconds, playlistSeconds }: { downloadStartSeconds?: number; playlistSeconds?: number } = {}) {
    const headroomMs = 30000;
    if (typeof downloadStartSeconds === 'number' && Number.isFinite(downloadStartSeconds) && downloadStartSeconds > 0) {
      this.downloadStartTimeout = downloadStartSeconds * 1000 + headroomMs;
    }
    if (typeof playlistSeconds === 'number' && Number.isFinite(playlistSeconds) && playlistSeconds > 0) {
      this.playlistTimeout = playlistSeconds * 1000 + headroomMs;
    }
  }

  async request<T = unknown>(endpoint: string, options: RequestInit & { timeout?: number } = {}): Promise<T> {
    const url = `${this.baseUrl}/api${endpoint}`;

    const defaultOptions = {
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: this.timeout,
    };

    const mergedOptions = { ...defaultOptions, ...options };

    // Add timeout support
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), mergedOptions.timeout);

    try {
      const response = await fetch(url, {
        ...mergedOptions,
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({})) as Record<string, unknown>;
        const message = typeof errorData.message === 'string' ? errorData.message
          : typeof errorData.error === 'string' ? errorData.error
          : `HTTP ${response.status}: ${response.statusText}`;
        const err = Object.assign(new Error(message), { code: errorData.code, status: response.status });
        throw err;
      }

      // Handle empty responses (like 204 No Content)
      if (response.status === 204 || response.headers.get('content-length') === '0') {
        return {} as T;
      }

      // Check if response has content to parse
      const contentType = response.headers.get('content-type');
      if (contentType && contentType.includes('application/json')) {
        return await response.json() as T;
      }

      // For non-JSON responses, return empty object
      return {} as T;
    } catch (error) {
      clearTimeout(timeoutId);

      if (error instanceof Error && error.name === 'AbortError') {
        throw new Error('Request timed out', { cause: error });
      }

      throw error;
    }
  }

  // Download management
  async getDownloads() {
    return this.request('/downloads');
  }

  async startDownload(downloadData: unknown) {
    return this.request('/downloads', {
      method: 'POST',
      body: JSON.stringify(downloadData),
      // Starting a download fetches video info server-side; allow headroom so a
      // slow network does not abort while the server succeeds.
      timeout: this.downloadStartTimeout,
    });
  }

  async startPlaylistDownload(url: string, type = 'video', quality = 'best', format = 'mp4') {
    return this.request('/downloads/playlist', {
      method: 'POST',
      body: JSON.stringify({ url, type, quality, format }),
      // Server enumerates the playlist; stay above that so the client does not
      // abort with a misleading timeout while the server adds the download.
      timeout: this.playlistTimeout,
    });
  }

  async downloadFirstVideo(url: string, type = 'video', quality = 'best', format = 'mp4') {
    return this.request('/downloads/first-video', {
      method: 'POST',
      body: JSON.stringify({ url, type, quality, format }),
      // Server enumerates the playlist to find the first item.
      timeout: this.playlistTimeout,
    });
  }

  async cancelDownload(id: string) {
    return this.request(`/downloads/${id}/cancel`, {
      method: 'POST',
    });
  }

  async pauseDownload(id: string) {
    return this.request(`/downloads/${id}/pause`, {
      method: 'POST',
    });
  }

  async resumeDownload(id: string) {
    return this.request(`/downloads/${id}/resume`, {
      method: 'POST',
    });
  }

  async retryDownload(id: string) {
    return this.request(`/downloads/${id}/retry`, {
      method: 'POST',
    });
  }

  async convertDownload(id: string) {
    return this.request(`/downloads/${id}/convert`, {
      method: 'POST',
    });
  }

  async removeDownload(id: string) {
    return this.request(`/downloads/${id}`, {
      method: 'DELETE',
    });
  }

  async downloadFile(id: string) {
    // Stream through the browser's native download manager instead of buffering
    // the whole file into memory with fetch()+blob(). For multi-GB files the
    // blob approach freezes the tab and shows no progress until the entire file
    // is in RAM, then the save dialog appears all at once. A direct navigation
    // lets the browser stream straight to disk with its own progress UI. The
    // server sets Content-Disposition: attachment and Content-Length, so the
    // filename and progress bar come from the response headers.
    const link = document.createElement('a');
    link.href = `${this.baseUrl}/api/downloads/${id}/download`;
    link.rel = 'noopener';
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);

    return { success: true };
  }

  // Bulk operations
  async clearQueuedDownloads() {
    return this.request('/downloads/clear-queued', {
      method: 'POST',
    });
  }

  async deleteCompletedDownloads() {
    return this.request('/downloads/delete-completed', {
      method: 'POST',
    });
  }

  async clearFailedDownloads() {
    return this.request('/downloads/clear-failed', {
      method: 'POST',
    });
  }

  // URL validation
  async validateUrl(url: string, type = 'video', quality = 'best', format = 'mp4') {
    return this.request<URLValidationResult>('/validate', {
      method: 'POST',
      body: JSON.stringify({ url, type, quality, format }),
      // Longer than the server-side validation timeout (playlist enumeration)
      // so a slow network does not abort before the backend responds.
      timeout: this.playlistTimeout,
    });
  }

  // Cookies file management
  async getCookiesStatus() {
    return this.request('/cookies');
  }

  async uploadCookies(file: File) {
    const formData = new FormData();
    formData.append('file', file);
    const response = await fetch(`${this.baseUrl}/api/cookies`, {
      method: 'POST',
      body: formData, // browser sets multipart Content-Type with boundary
    });
    if (!response.ok) {
      const errorData = await response.json().catch(() => ({})) as Record<string, unknown>;
      const message = typeof errorData.message === 'string' ? errorData.message
        : typeof errorData.error === 'string' ? errorData.error
        : `HTTP ${response.status}`;
      const err = Object.assign(new Error(message), { code: errorData.code, status: response.status });
      throw err;
    }
    return response.json();
  }

  async removeCookies() {
    return this.request('/cookies', { method: 'DELETE' });
  }

  // Configuration
  async getConfig() {
    return this.request('/config');
  }

  async updateConfig(config: Record<string, unknown>) {
    return this.request('/config', {
      method: 'POST',
      body: JSON.stringify(config),
    });
  }

  // System information
  async getVersions() {
    return this.request('/versions');
  }

  async getYtDlpVersion() {
    return this.request('/yt-dlp/version');
  }

  async updateYtDlp() {
    return this.request('/yt-dlp/update', {
      method: 'POST',
      timeout: 60000, // 60 seconds for update
    });
  }

  // Health check
  async healthCheck() {
    try {
      const response = await fetch(`${this.baseUrl}/api/config`, { method: 'HEAD' });
      return response.ok;
    } catch {
      return false;
    }
  }

  // Utility methods
  setBaseUrl(url: string) {
    this.baseUrl = url;
  }

  setTimeout(timeout: number) {
    this.timeout = timeout;
  }
}
