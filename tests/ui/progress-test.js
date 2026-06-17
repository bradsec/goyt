/**
 * Progress Bar Testing Suite for goyt
 * Using Puppeteer to test download progress updates
 */

import puppeteer from 'puppeteer';
import { spawn } from 'child_process';
import fs from 'fs';
import { fileURLToPath } from 'url';
import { dirname } from 'path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

class ProgressTestSuite {
  constructor() {
    this.browser = null;
    this.page = null;
    this.server = null;
    this.baseURL = 'http://localhost:3001';
    this.testResults = [];
  }

  async setup() {
    console.log('Setting up progress test environment...');
    
    await this.startTestServer();
    
    this.browser = await puppeteer.launch({
      headless: false, // Keep visible to see progress bars
      args: ['--no-sandbox', '--disable-setuid-sandbox'],
      slowMo: 100 // Slow down for better observation
    });
    
    this.page = await this.browser.newPage();
    await this.page.setViewport({ width: 1920, height: 1080 });
    
    // Enable console logging
    this.page.on('console', msg => console.log('PAGE:', msg.text()));
    this.page.on('pageerror', err => console.log('PAGE ERROR:', err.message));
    
    console.log('Progress test environment ready');
  }

  async startTestServer() {
    return new Promise((resolve, reject) => {
      console.log('Starting test server...');
      
      const testConfig = {
        "download_path": "./test-downloads",
        "max_concurrent_downloads": 1,
        "yt_dlp_path": "./assets/yt-dlp/yt-dlp",
        "ffmpeg_path": "ffmpeg",
        "port": 3001,
        "default_video_format": "mp4",
        "default_audio_format": "mp3",
        "default_video_quality": "360p", // Use lower quality for faster testing
        "verbose_logging": true, // Enable to see progress output
        "completed_file_expiry_hours": 1,
        "enable_hardware_acceleration": false,
        "optimize_for_low_power": true
      };
      
      fs.writeFileSync('./test-config.json', JSON.stringify(testConfig, null, 2));
      
      this.server = spawn('./goyt', ['-config', 'test-config.json'], {
        cwd: process.cwd(),
        stdio: 'pipe'
      });
      
      // The readiness line is emitted by the standard logger, which writes to
      // stderr, so watch both streams and match the actual "listening on" text.
      let ready = false;
      const watchForReady = (output) => {
        if (!ready && output.includes('listening on')) {
          ready = true;
          setTimeout(resolve, 3000);
        }
      };

      this.server.stdout.on('data', (data) => {
        const output = data.toString();
        console.log('SERVER:', output.trim());
        watchForReady(output);
      });

      this.server.stderr.on('data', (data) => {
        const output = data.toString();
        console.log('SERVER ERROR:', output);
        watchForReady(output);
      });
      
      this.server.on('error', reject);
      setTimeout(() => reject(new Error('Server start timeout')), 45000);
    });
  }

  async teardown() {
    console.log('Cleaning up...');
    
    if (this.page) await this.page.close();
    if (this.browser) await this.browser.close();
    if (this.server) {
      this.server.kill('SIGTERM');
      await new Promise(resolve => setTimeout(resolve, 2000));
    }
    
    try {
      fs.unlinkSync('./test-config.json');
      if (fs.existsSync('./test-downloads')) {
        fs.rmSync('./test-downloads', { recursive: true, force: true });
      }
    } catch (err) {
      console.log('Cleanup warning:', err.message);
    }
  }

  async testProgressBarUpdates() {
    console.log('Testing progress bar updates...');
    
    await this.page.goto(this.baseURL);
    await this.page.waitForSelector('#download-form', { timeout: 15000 });
    
    // Test with a short video that should show progress
    const testURL = 'https://www.youtube.com/watch?v=dQw4w9WgXcQ'; // Rick Roll - short video
    
    console.log('Entering test URL:', testURL);
    await this.page.type('#url-input', testURL);
    
    // Set to 360p for faster download
    await this.page.select('#quality-select', '360p');
    
    console.log('Starting download...');
    await this.page.click('#submit-button');
    
    // Wait for download to appear in the list  
    await this.page.waitForSelector('.downloads-section .download-item, .download-entry, [data-download-id]', { timeout: 20000 });
    
    console.log('Monitoring progress updates...');
    
    const progressUpdates = [];
    let lastProgress = -1;
    let maxWaitTime = 120000; // 2 minutes max
    let startTime = Date.now();
    
    // Monitor progress updates
    while (Date.now() - startTime < maxWaitTime) {
      try {
        const progressBars = await this.page.$$('.progress-bar, .progress-fill, [data-progress], .download-progress, .progress');
        
        for (const progressBar of progressBars) {
          const progressText = await this.page.evaluate(el => {
            // Try different ways to get progress
            const text = el.textContent || el.innerText || '';
            const style = getComputedStyle(el);
            const width = style.width;
            const ariaValue = el.getAttribute('aria-valuenow');
            const dataProgress = el.getAttribute('data-progress');
            
            return {
              text: text.trim(),
              width: width,
              ariaValue: ariaValue,
              dataProgress: dataProgress,
              className: el.className
            };
          }, progressBar);
          
          // Extract percentage from various sources
          let currentProgress = null;
          
          if (progressText.ariaValue) {
            currentProgress = parseFloat(progressText.ariaValue);
          } else if (progressText.dataProgress) {
            currentProgress = parseFloat(progressText.dataProgress);
          } else if (progressText.text.includes('%')) {
            const match = progressText.text.match(/(\\d+(?:\\.\\d+)?)%/);
            if (match) currentProgress = parseFloat(match[1]);
          } else if (progressText.width && progressText.width !== 'auto') {
            const match = progressText.width.match(/(\\d+(?:\\.\\d+)?)%/);
            if (match) currentProgress = parseFloat(match[1]);
          }
          
          if (currentProgress !== null && currentProgress !== lastProgress) {
            console.log(`Progress update: ${currentProgress}%`);
            progressUpdates.push({
              timestamp: Date.now() - startTime,
              progress: currentProgress,
              details: progressText
            });
            lastProgress = currentProgress;
            
            if (currentProgress >= 100) {
              console.log('Download completed!');
              return progressUpdates;
            }
          }
        }
        
        // Check if download failed or completed
        const statusElements = await this.page.$$('.download-status, .status');
        for (const statusEl of statusElements) {
          const status = await this.page.evaluate(el => el.textContent, statusEl);
          if (status.includes('completed') || status.includes('failed') || status.includes('error')) {
            console.log(`Download status: ${status}`);
            return progressUpdates;
          }
        }
        
        await new Promise(resolve => setTimeout(resolve, 1000)); // Wait 1 second
        
      } catch (error) {
        console.log('Progress monitoring error:', error.message);
        await new Promise(resolve => setTimeout(resolve, 2000));
      }
    }
    
    console.log('Progress monitoring timed out');
    return progressUpdates;
  }

  async testAPIProgressEndpoint() {
    console.log('Testing API progress endpoint...');
    
    const response = await this.page.evaluate(async (baseURL) => {
      try {
        const res = await fetch(baseURL + '/api/downloads');
        const downloads = await res.json();
        return downloads;
      } catch (error) {
        return { error: error.message };
      }
    }, this.baseURL);
    
    console.log('API Response:', JSON.stringify(response, null, 2));
    return response;
  }

  async runProgressTests() {
    try {
      await this.setup();
      
      console.log('\\nStarting Progress Bar Test Suite');
      console.log('='.repeat(50));
      
      // Test API endpoint first
      const apiResponse = await this.testAPIProgressEndpoint();
      console.log('API test completed');
      
      // Test progress bar updates
      const progressUpdates = await this.testProgressBarUpdates();
      
      console.log('\\nProgress Test Results:');
      console.log('='.repeat(30));
      console.log(`Total progress updates captured: ${progressUpdates.length}`);
      
      if (progressUpdates.length > 0) {
        console.log('Progress updates are working!');
        console.log('Progress timeline:');
        progressUpdates.forEach((update, i) => {
          console.log(`  ${i + 1}. ${update.timestamp}ms: ${update.progress}%`);
        });
      } else {
        console.log('No progress updates detected - progress bar may be broken');
      }
      
      // Generate report
      const report = {
        timestamp: new Date().toISOString(),
        progressUpdatesDetected: progressUpdates.length,
        progressTimeline: progressUpdates,
        apiResponse: apiResponse,
        testResult: progressUpdates.length > 0 ? 'PASS' : 'FAIL'
      };
      
      fs.writeFileSync('./progress-test-report.json', JSON.stringify(report, null, 2));
      console.log('\\nDetailed report saved to progress-test-report.json');
      
      return report.testResult === 'PASS';
      
    } finally {
      await this.teardown();
    }
  }
}

// Run tests if called directly
if (import.meta.url === `file://${process.argv[1]}`) {
  const testSuite = new ProgressTestSuite();
  testSuite.runProgressTests()
    .then(success => {
      console.log(success ? '\\nProgress tests PASSED!' : '\\nProgress tests FAILED!');
      process.exit(success ? 0 : 1);
    })
    .catch(error => {
      console.error('Progress test suite failed:', error);
      process.exit(1);
    });
}

export default ProgressTestSuite;