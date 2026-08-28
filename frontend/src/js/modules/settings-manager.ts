/**
 * Settings Manager Module
 * Handles application settings and configuration
 */

import type { ApiClient } from './api-client.js';

type SettingValue = string | number | boolean | null | undefined;

interface Settings extends Record<string, SettingValue> {
  auth_enabled?: boolean;
  version?: string;
  port?: number;
  max_concurrent_downloads?: number;
  completed_file_expiry_hours?: number;
  playlist_load_timeout_seconds?: number;
  download_start_timeout_seconds?: number;
}

interface Versions extends Record<string, string | undefined> {
  yt_dlp?: string;
  ffmpeg?: string;
}

interface CookiesStatus {
  present: boolean;
  modified?: string;
  path: string;
}

interface UpdateInfo {
  update_available: boolean;
  current_version: string;
  latest_version: string;
}

interface UpdateResult {
  warnings?: unknown;
}

interface SettingsUI {
  showNotification(message: string, type?: string, duration?: number | null): void;
  setLoading(elementId: string, loading: boolean, text?: string): void;
}

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);

export class SettingsManager {
  apiClient: ApiClient;
  uiManager: SettingsUI;
  settings: Settings;
  versions: Versions;
  isPanelOpen: boolean;
  logoutWired = false;
  cookieControlsWired = false;

  constructor(apiClient: ApiClient, uiManager: SettingsUI) {
    this.apiClient = apiClient;
    this.uiManager = uiManager;
    this.settings = {};
    this.versions = {};
    this.isPanelOpen = false;
  }

  async init(): Promise<void> {
    this.setupEventListeners();
    await this.loadSettings();
  }

  setupEventListeners(): void {
    // Settings form submission
    const settingsForm = document.getElementById('settings-form');
    if (settingsForm) {
      settingsForm.addEventListener('submit', this.handleSettingsSubmit.bind(this));
    }

    // Reset to defaults button
    const resetButton = document.getElementById('reset-settings');
    if (resetButton) {
      resetButton.addEventListener('click', this.resetToDefaults.bind(this));
    }

    // Settings panel close
    const closeButton = document.getElementById('settings-close');
    if (closeButton) {
      closeButton.addEventListener('click', () => this.closePanel());
    }

    // Settings overlay click
    const overlay = document.getElementById('settings-overlay');
    if (overlay) {
      overlay.addEventListener('click', () => this.closePanel());
    }

    // Update buttons
    const updateButton = document.getElementById('update-ytdlp');
    if (updateButton) {
      updateButton.addEventListener('click', this.handleYtDlpUpdate.bind(this));
    }

    const checkUpdateButton = document.getElementById('check-updates');
    if (checkUpdateButton) {
      checkUpdateButton.addEventListener('click', this.checkForUpdates.bind(this));
    }

    // ESC key to close panel
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && this.isPanelOpen) {
        this.closePanel();
      }
    });
  }

  async loadSettings(): Promise<void> {
    try {
      const [settings, versions] = await Promise.all([
        this.apiClient.getConfig(),
        this.apiClient.getVersions()
      ]);

      this.settings = settings as Settings;
      this.versions = versions as Versions;

      this.applyClientTimeouts();
      this.updateSettingsForm();
      this.updateVersionInfo();
      this.refreshCookiesStatus();
      this.wireCookieControls();
      this.setupLogout();
    } catch (error) {
      console.error('Failed to load settings:', error);
      this.uiManager.showNotification('Failed to load settings', 'error');
    }
  }

  // Reveal and wire the sign-out control only when auth is enabled.
  setupLogout(): void {
    const logoutBtn = document.getElementById('logout-btn');
    if (!logoutBtn || !this.settings?.auth_enabled || this.logoutWired) {
      return;
    }
    this.logoutWired = true;
    logoutBtn.hidden = false;
    logoutBtn.addEventListener('click', async () => {
      try {
        await fetch('/api/logout', { method: 'POST' });
      } catch (error) {
        console.error('Logout request failed:', error);
      }
      window.location.href = '/login';
    });
  }

  // Push the configured network timeouts onto the API client so its fetch
  // aborts stay above the matching server-side timeouts.
  applyClientTimeouts(): void {
    this.apiClient.setActionTimeouts({
      downloadStartSeconds: Number(this.settings.download_start_timeout_seconds),
      playlistSeconds: Number(this.settings.playlist_load_timeout_seconds),
    });
  }

  updateSettingsForm(): void {
    const form = document.getElementById('settings-form') as HTMLFormElement | null;
    if (!form) return;

    // Update form fields
    Object.entries(this.settings).forEach(([key, value]) => {
      const element = form.elements.namedItem(key);
      if (element instanceof HTMLInputElement) {
        if (element.type === 'checkbox') {
          element.checked = Boolean(value);
        } else {
          element.value = value == null ? '' : String(value);
        }
      } else if (element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) {
        element.value = value == null ? '' : String(value);
      }
    });
  }

  updateVersionInfo(): void {
    // Update yt-dlp version
    const ytdlpVersion = document.getElementById('ytdlp-version');
    if (ytdlpVersion) {
      ytdlpVersion.textContent = this.versions.yt_dlp || 'Not available';
    }

    // Update ffmpeg version
    const ffmpegVersion = document.getElementById('ffmpeg-version');
    if (ffmpegVersion) {
      ffmpegVersion.textContent = this.versions.ffmpeg || 'Not available';
    }

    // Update footer versions
    const footerAppVersion = document.getElementById('footer-app-version');
    if (footerAppVersion) {
      footerAppVersion.textContent = this.settings.version || '';
    }
    const footerYtdlpVersion = document.getElementById('footer-ytdlp-version');
    if (footerYtdlpVersion) {
      footerYtdlpVersion.textContent = this.versions.yt_dlp || '';
    }
    const footerFfmpegVersion = document.getElementById('footer-ffmpeg-version');
    if (footerFfmpegVersion) {
      footerFfmpegVersion.textContent = this.versions.ffmpeg || '';
    }
  }

  wireCookieControls(): void {
    if (this.cookieControlsWired) return;
    const uploadBtn = document.getElementById('cookies_upload_btn');
    const removeBtn = document.getElementById('cookies_remove_btn');
    const fileInput = document.getElementById('cookies_file_input') as HTMLInputElement | null;
    if (!uploadBtn || !removeBtn || !fileInput) return;

    // The Upload button opens the (hidden) file picker; selecting a file
    // uploads it immediately, so it is a single action for the user.
    uploadBtn.addEventListener('click', () => fileInput.click());
    fileInput.addEventListener('change', async () => {
      const file = fileInput.files && fileInput.files[0];
      if (!file) return;
      try {
        await this.apiClient.uploadCookies(file);
        this.uiManager.showNotification('Cookies file uploaded', 'success');
      } catch (error) {
        this.uiManager.showNotification(errorMessage(error) || 'Cookies upload failed', 'error');
      } finally {
        fileInput.value = '';
        this.refreshCookiesStatus();
      }
    });

    removeBtn.addEventListener('click', async () => {
      try {
        await this.apiClient.removeCookies();
        this.uiManager.showNotification('Cookies file removed', 'success');
        this.refreshCookiesStatus();
      } catch (error) {
        this.uiManager.showNotification(errorMessage(error) || 'Failed to remove cookies', 'error');
      }
    });

    this.cookieControlsWired = true;
  }

  async refreshCookiesStatus(): Promise<void> {
    const el = document.getElementById('cookies_status');
    const removeBtn = document.getElementById('cookies_remove_btn');
    if (!el) return;
    try {
      const status = await this.apiClient.getCookiesStatus() as CookiesStatus;
      if (status.present) {
        const when = status.modified ? new Date(status.modified).toLocaleString() : 'unknown';
        el.textContent = `Cookies file present at ${status.path} · updated ${when}`;
        if (removeBtn) removeBtn.style.display = '';
      } else {
        el.textContent = 'No cookies file uploaded.';
        if (removeBtn) removeBtn.style.display = 'none';
      }
    } catch {
      el.textContent = 'Could not check cookies file status.';
      if (removeBtn) removeBtn.style.display = 'none';
    }
  }

  async handleSettingsSubmit(event: SubmitEvent): Promise<void> {
    event.preventDefault();

    const form = event.currentTarget;
    if (!(form instanceof HTMLFormElement)) return;
    const formData = new FormData(form);
    const newSettings: Settings = {};

    // Extract form data
    for (const [key, value] of formData.entries()) {
      if (key in this.settings) {
        // Convert numeric strings to numbers
        if (key === 'port' || key === 'max_concurrent_downloads' || key === 'completed_file_expiry_hours'
            || key === 'playlist_load_timeout_seconds' || key === 'download_start_timeout_seconds') {
          newSettings[key] = parseInt(String(value), 10);
        } else {
          newSettings[key] = String(value);
        }
      }
    }

    // Handle checkboxes (they won't be in formData if unchecked)
    const checkboxFields = ['verbose_logging', 'enable_hardware_acceleration', 'optimize_for_low_power', 'reencode_for_compatibility'];
    checkboxFields.forEach(field => {
      if (field in this.settings) {
        newSettings[field] = formData.has(field);
      }
    });

    const portChanged = 'port' in newSettings && newSettings.port !== this.settings.port;

    try {
      this.uiManager.setLoading('save-settings', true, 'Saving...');

      const result = await this.apiClient.updateConfig(newSettings) as UpdateResult;
      this.settings = { ...this.settings, ...newSettings };
      this.applyClientTimeouts();

      this.uiManager.showNotification('Settings saved successfully', 'success');

      if (result && Array.isArray(result.warnings)) {
        for (const warning of result.warnings.filter((value): value is string => typeof value === 'string')) {
          this.uiManager.showNotification(warning, 'warning', 10000);
        }
      }

      if (portChanged) {
        this.uiManager.showNotification(
          'Port change requires restart to take effect',
          'warning',
          10000
        );
      }

      // Close the settings panel on a successful save; the notifications above
      // render in their own container and stay visible.
      this.closePanel();

    } catch (error) {
      console.error('Failed to save settings:', error);
      this.uiManager.showNotification(`Failed to save settings: ${errorMessage(error)}`, 'error');
    } finally {
      this.uiManager.setLoading('save-settings', false);
    }
  }

  async checkForUpdates(): Promise<void> {
    try {
      this.uiManager.setLoading('check-updates', true, 'Checking...');

      const updateInfo = await this.apiClient.getYtDlpVersion() as UpdateInfo;

      if (updateInfo.update_available) {
        this.uiManager.showNotification(
          `yt-dlp update available: ${updateInfo.current_version} → ${updateInfo.latest_version}`,
          'info',
          10000
        );

        // Enable update button
        const updateButton = document.getElementById('update-ytdlp') as HTMLButtonElement | null;
        if (updateButton) {
          updateButton.disabled = false;
          updateButton.textContent = 'Update Available';
          updateButton.classList.add('btn-warning');
        }
      } else {
        this.uiManager.showNotification('yt-dlp is up to date', 'success');
      }

    } catch (error) {
      console.error('Failed to check for updates:', error);
      this.uiManager.showNotification(`Failed to check for updates: ${errorMessage(error)}`, 'error');
    } finally {
      this.uiManager.setLoading('check-updates', false);
    }
  }

  async handleYtDlpUpdate(): Promise<void> {
    if (!confirm('This will update yt-dlp to the latest version. Continue?')) {
      return;
    }

    try {
      this.uiManager.setLoading('update-ytdlp', true, 'Updating...');

      await this.apiClient.updateYtDlp();

      this.uiManager.showNotification('yt-dlp updated successfully', 'success');

      // Refresh version info
      await this.loadVersions();

      // Reset update button
      const updateButton = document.getElementById('update-ytdlp') as HTMLButtonElement | null;
      if (updateButton) {
        updateButton.disabled = true;
        updateButton.textContent = 'Update yt-dlp';
        updateButton.classList.remove('btn-warning');
      }

    } catch (error) {
      console.error('Failed to update yt-dlp:', error);
      this.uiManager.showNotification(`Failed to update yt-dlp: ${errorMessage(error)}`, 'error');
    } finally {
      this.uiManager.setLoading('update-ytdlp', false);
    }
  }

  async loadVersions(): Promise<void> {
    try {
      this.versions = await this.apiClient.getVersions() as Versions;
      this.updateVersionInfo();
    } catch (error) {
      console.error('Failed to load versions:', error);
    }
  }

  togglePanel(): void {
    if (this.isPanelOpen) {
      this.closePanel();
    } else {
      this.openPanel();
    }
  }

  openPanel(): void {
    const panel = document.getElementById('settings-panel');
    const overlay = document.getElementById('settings-overlay');

    if (panel && overlay) {
      panel.classList.add('show');
      overlay.classList.add('show');
      this.isPanelOpen = true;

      // Focus first input
      const firstInput = panel.querySelector<HTMLElement>('input, select, textarea');
      if (firstInput) {
        setTimeout(() => firstInput.focus(), 300);
      }
    }
  }

  closePanel(): void {
    const panel = document.getElementById('settings-panel');
    const overlay = document.getElementById('settings-overlay');

    if (panel && overlay) {
      panel.classList.remove('show');
      overlay.classList.remove('show');
      this.isPanelOpen = false;
    }
  }

  // Validation helpers
  validateSettings(settings: Settings): string[] {
    const errors: string[] = [];

    if (settings.port && (settings.port < 1 || settings.port > 65535)) {
      errors.push('Port must be between 1 and 65535');
    }

    if (settings.max_concurrent_downloads && settings.max_concurrent_downloads < 1) {
      errors.push('Max concurrent downloads must be at least 1');
    }

    if (settings.completed_file_expiry_hours && settings.completed_file_expiry_hours < 0) {
      errors.push('File expiry hours cannot be negative');
    }

    return errors;
  }

  // Export/Import settings (for future use)
  exportSettings(): void {
    const settingsBlob = new Blob([JSON.stringify(this.settings, null, 2)], {
      type: 'application/json'
    });

    const url = URL.createObjectURL(settingsBlob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'goyt-settings.json';
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
  }

  async importSettings(file: File): Promise<void> {
    try {
      const text = await file.text();
      const importedSettings = JSON.parse(text) as Settings;

      // Validate imported settings
      const errors = this.validateSettings(importedSettings);
      if (errors.length > 0) {
        throw new Error(`Invalid settings: ${errors.join(', ')}`);
      }

      // Update settings
      await this.apiClient.updateConfig(importedSettings);
      this.settings = importedSettings;
      this.updateSettingsForm();

      this.uiManager.showNotification('Settings imported successfully', 'success');
    } catch (error) {
      this.uiManager.showNotification(`Failed to import settings: ${errorMessage(error)}`, 'error');
    }
  }

  // Reset to defaults
  async resetToDefaults(): Promise<void> {
    if (!confirm('Are you sure you want to reset all settings to defaults? This cannot be undone.')) {
      return;
    }

    const defaultSettings = {
      download_path: './downloads',
      max_concurrent_downloads: 3,
      cookies_file_path: '',
      port: 3000,
      default_video_format: 'mp4',
      default_audio_format: 'mp3',
      default_video_quality: '1080p',
      verbose_logging: false,
      completed_file_expiry_hours: 72,
      enable_hardware_acceleration: false,
      optimize_for_low_power: false,
      reencode_for_compatibility: false,
      playlist_load_timeout_seconds: 180,
      download_start_timeout_seconds: 60
    };

    try {
      await this.apiClient.updateConfig(defaultSettings);
      this.settings = defaultSettings;
      this.applyClientTimeouts();
      this.updateSettingsForm();

      this.uiManager.showNotification('Settings reset to defaults', 'success');
    } catch (error) {
      this.uiManager.showNotification(`Failed to reset settings: ${errorMessage(error)}`, 'error');
    }
  }

  // Utility methods
  getSetting(key: string, defaultValue: SettingValue = null): SettingValue {
    return this.settings[key] ?? defaultValue;
  }

  getVersion(component: string): string {
    return this.versions[component] || 'Unknown';
  }

  // Cleanup
  cleanup(): void {
    this.closePanel();
  }
}
