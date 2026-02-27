const {app, BrowserWindow, globalShortcut, Notification, powerSaveBlocker, dialog, shell } = require('electron/main');
const path = require('node:path');
const {ipcMain} = require('electron')
const http = require('http');
const https = require('https');
const xml = require("xml2js");
const net = require('net');
const WebSocket = require('ws');
const fs = require('fs');
const forge = require('node-forge');
const httpolyglot = require('httpolyglot');

// In some cases we need to make the WLgate window resizable (for example for tiling window managers)
// Default: false
const resizable = process.env.WLGATE_RESIZABLE === 'true' || false;
const sleepable = process.env.WLGATE_SLEEP === 'true' || false;

const gotTheLock = app.requestSingleInstanceLock();

let powerSaveBlockerId;
let s_mainWindow;
let certInstallWindow;
let pendingCertInstall = false; // Track if cert install needs to be shown
let msgbacklog=[];
let qsyServer; // Dual-mode HTTP/HTTPS server for QSY
let currentCAT=null;
var WServer;
let udpServer = null; // UDP server for ADIF/WSJT-X
let wsServer;
let wsClients = new Set();
let wssServer; // Secure WebSocket server
let wssClients = new Set(); // Secure WebSocket clients
let wssHttpsServer; // HTTPS server for secure WebSocket
let isShuttingDown = false;
let activeConnections = new Set(); // Track active TCP connections
let activeHttpRequests = new Set(); // Track active HTTP requests for cancellation
let rotatorFollowMode = 'off'; // 'off', 'hf', 'sat' — runtime only, not persisted

// Rotator state
// Protocol (observed on real hardware):
//   P az el\n  →  [current_az\ncurrent_el\n] × N  then  RPRT 0\n
//   p\n        →  az\nel\n  (no RPRT)  — but HANGS with no response when idle on some backends
//   S\n        →  RPRT 0\n  (halt — sent directly, bypasses queue)
//
// Important: some backends never respond to `p` until at least one `P` has been sent.
// rotatorHasSentP gates the poll timer so we don't block the queue on connect.
let rotatorSocket      = null;
let rotatorConnecting  = false;
let rotatorConnectedTo = null;
let rotatorBusy        = false;   // waiting for a response
let rotatorBusyTimer   = null;    // watchdog — clears stuck rotatorBusy after 5 s
let rotatorBuffer      = '';      // accumulates incoming bytes
let rotatorCurrentCmd  = null;    // 'set' | 'get'
let rotatorPendingSet  = null;    // { az, el } — latest P not yet sent
let rotatorPollPending = false;   // p query queued
let rotatorPollTimer   = null;
let rotatorHasSentP    = false;   // gate: don't poll until first P has been sent
let rotatorLastPTime   = 0;       // ms timestamp of last P send — poll suppressed for 3 s after
let rotatorLastCmdAz   = null;    // last commanded azimuth — for direction reversal detection
let rotatorLastCmdEl   = null;    // last commanded elevation — for direction reversal detection
let rotatorCurrentAz   = null;    // current real position from polls
let rotatorCurrentEl   = null;    // current real position from polls
let rotatorStopping    = false;   // true when we've sent S and are waiting for RPRT before sending P
let rotatorStopAfterRPRT = null;  // { az, el } — P to send after S completes

// Certificate paths for HTTPS server
let certPaths = {
	key: null,
	cert: null
};

const DemoAdif='<call:5>DJ7NT <gridsquare:4>JO30 <mode:3>FT8 <rst_sent:3>-15 <rst_rcvd:2>33 <qso_date:8>20240110 <time_on:6>051855 <qso_date_off:8>20240110 <time_off:6>051855 <band:3>40m <freq:8>7.155783 <station_callsign:5>TE1ST <my_gridsquare:6>JO30OO <eor>';

if (require('electron-squirrel-startup')) app.quit();

const udp = require('dgram');

let q={};
let defaultcfg = {
	wavelog_url: "https://log.jo30.de/index.php",
	wavelog_key: "mykey",
	wavelog_id: "0",
	wavelog_radioname: 'WLGate',
	wavelog_pmode: true,
	flrig_host: '127.0.0.1',
	flrig_port: '12345',
	flrig_ena: false,
	hamlib_host: '127.0.0.1',
	hamlib_port: '4532',
	hamlib_ena: false,
	ignore_pwr: false,
	udp_enabled: true,      // Global UDP setting (not per-profile)
	udp_port: 2333,          // Global UDP port (not per-profile)
}

const storage = require('electron-json-storage');

// =============================================================================
// Simple Update Checker
// =============================================================================

// Get repository info from package.json
function getRepoInfo() {
	try {
		const pkg = require('./package.json');
		if (pkg.repository && pkg.repository.url) {
			const match = pkg.repository.url.match(/github\.com[/:]([^/]+)\/([^/]+)/);
			if (match) {
				return { owner: match[1], repo: match[2].replace('.git', '') };
			}
		}
	} catch (e) {
		console.log('Could not read repository info:', e.message);
	}
	// Fallback to defaults
	return { owner: 'wavelog', repo: 'WaveLogGate' };
}

// Compare two version strings (returns true if v2 > v1)
function isNewerVersion(v1, v2) {
	const parts1 = v1.split('.').map(Number);
	const parts2 = v2.split('.').map(Number);
	for (let i = 0; i < Math.max(parts1.length, parts2.length); i++) {
		const p1 = parts1[i] || 0;
		const p2 = parts2[i] || 0;
		if (p2 > p1) return true;
		if (p2 < p1) return false;
	}
	return false;
}

// Check for updates via GitHub API
function checkForUpdates() {
	if (!app.isPackaged) {
		console.log('Skipping update check (development mode)');
		return;
	}

	const repoInfo = getRepoInfo();
	const currentVersion = app.getVersion();

	console.log(`Checking for updates (current: ${currentVersion})...`);

	const options = {
		hostname: 'api.github.com',
		path: `/repos/${repoInfo.owner}/${repoInfo.repo}/releases/latest`,
		headers: {
			'User-Agent': 'WaveLogGate'
		}
	};

	https.get(options, (res) => {
		let data = '';

		res.on('data', (chunk) => {
			data += chunk;
		});

		res.on('end', () => {
			try {
				const release = JSON.parse(data);
				const latestVersion = release.tag_name.replace(/^v/, '');

				console.log(`Latest version: ${latestVersion}`);

				if (isNewerVersion(currentVersion, latestVersion)) {
					console.log(`Update available: ${latestVersion}`);
					showUpdateNotification(latestVersion, release.html_url);
				} else {
					console.log('Already up to date');
				}
			} catch (e) {
				console.error('Error parsing release info:', e.message);
			}
		});
	}).on('error', (err) => {
		console.error('Error checking for updates:', err.message);
	});
}

// Show notification about available update
function showUpdateNotification(version, releaseUrl) {
	// On Windows, use dialog because notification clicks don't work reliably
	if (process.platform === 'win32') {
		dialog.showMessageBox({
			type: 'info',
			title: 'WaveLogGate Update Available',
			message: `A new version is available!`,
			detail: `Version ${version} is ready to download. You are currently running v${app.getVersion()}.`,
			buttons: ['Go to Download', 'Later'],
			defaultId: 0,
			cancelId: 1
		}).then(result => {
			if (result.response === 0) {
				console.log('Opening download page:', releaseUrl);
				shell.openExternal(releaseUrl);
			}
		});
		return;
	}

	// On macOS/Linux, use native notification (click works on macOS)
	if (Notification.isSupported()) {
		const notification = new Notification({
			title: 'WaveLogGate Update Available',
			body: `Version ${version} is available. Click to download.`,
			icon: path.join(__dirname, 'icon.png'),
			silent: false
		});

		notification.once('click', () => {
			console.log('Notification clicked, opening:', releaseUrl);
			shell.openExternal(releaseUrl);
		});

		notification.show();
	} else {
		// Fallback: log to console
		console.log(`Update available: ${version} - Download from: ${releaseUrl}`);
	}
}

app.disableHardwareAcceleration(); 

function createWindow () {
	const mainWindow = new BrowserWindow({
		width: 430,
		height: 250,
		resizable: resizable, // Default: false, can be overwritten with WLGATE_RESIZABLE
		autoHideMenuBar: app.isPackaged,
		webPreferences: {
			contextIsolation: false,
			backgroundThrottling: false,
			nodeIntegration: true,
			devTools: !app.isPackaged,
			enableRemoteModule: true,
			preload: path.join(__dirname, 'preload.js')
		}
	});
	if (app.isPackaged) {
		mainWindow.setMenu(null);
	}


	mainWindow.loadFile('index.html')
	mainWindow.setTitle(require('./package.json').name + " V" + require('./package.json').version);

	return mainWindow;
}


ipcMain.on("set_config", async (event,arg) => {
	defaultcfg=arg;
	storage.set('basic', defaultcfg, function(e) {
		if (e) throw e;
	});
	event.returnValue=defaultcfg;
});

ipcMain.on("resize", async (event,arg) => {
	const newsize=arg;
	s_mainWindow.setContentSize(newsize.width,newsize.height,newsize.ani);
	s_mainWindow.setSize(newsize.width,newsize.height,newsize.ani);
	event.returnValue=true;
});

ipcMain.on("get_window_size", async (event) => {
	const size = s_mainWindow.getSize();
	event.returnValue = { width: size[0], height: size[1] };
});

ipcMain.on("get_config", async (event, arg) => {
	let storedcfg = storage.getSync('basic');
	let realcfg={};
	if (!(storedcfg.wavelog_url) && !(storedcfg.profiles)) { storedcfg=defaultcfg; }	// Old config not present, add default-cfg
	if (!(storedcfg.profiles)) {	// Old Config without array? Convert it
		(realcfg.profiles = realcfg.profiles || []).push(storedcfg);
		realcfg.profiles.push(defaultcfg);
		realcfg.profile=(storedcfg.profile ?? 0);
	} else {
		realcfg=storedcfg;
	}
	// Migration: Add version and profileNames for dynamic profile system
	if (!realcfg.version || realcfg.version < 2) {
		realcfg.version = 2;
		if (!realcfg.profileNames) {
			realcfg.profileNames = realcfg.profiles.map((_, i) => `Profile ${i + 1}`);
		}
		storage.set('basic', realcfg, function(e) {
			if (e) throw e;
		});
	}
	// Migration: Add global UDP settings (version 3)
	if (!realcfg.version || realcfg.version < 3) {
		realcfg.version = 3;
		// Add global UDP settings if not present
		if (realcfg.udp_enabled === undefined) {
			realcfg.udp_enabled = true;
		}
		if (realcfg.udp_port === undefined) {
			realcfg.udp_port = 2333;
		}
		storage.set('basic', realcfg, function(e) {
			if (e) throw e;
		});
	}
	if ((arg ?? '') !== '') {
		realcfg.profile=arg;
	}
	defaultcfg=realcfg;
	storage.set('basic', realcfg, function(e) {	// Store one time
		if (e) throw e;
	});
	event.returnValue = realcfg;
});

ipcMain.on("setCAT", async (event,arg) => {
	settrx(arg);
	event.returnValue=true;
});

ipcMain.on("quit", async (event,arg) => {
	console.log('Quit requested from renderer');
	shutdownApplication();
	app.quit();
	event.returnValue=true;
});

ipcMain.on("radio_status_update", async (event,arg) => {
	// Broadcast radio status updates from renderer to WebSocket clients
	broadcastRadioStatus(arg);
	event.returnValue=true;
});

ipcMain.on("get_ca_cert", async (event) => {
	// Return the CA certificate for display/installation
	const caCert = getCaCertificate();
	event.returnValue = caCert;
});

ipcMain.on("install_ca_cert", async (event) => {
	// Attempt to install the CA certificate
	const result = await installCertificate();
	event.returnValue = result;
});

ipcMain.on("get_cert_info", async (event) => {
	// Return certificate installation info for the UI
	const userDataPath = app.getPath('userData');
	const certPath = path.join(userDataPath, 'certs', 'server.crt');

	event.returnValue = {
		certPath: certPath,
		platform: process.platform,
		hasCert: fs.existsSync(certPath)
	};
});

ipcMain.on("close_cert_install_window", async () => {
	if (certInstallWindow && !certInstallWindow.isDestroyed()) {
		certInstallWindow.close();
	}
});

ipcMain.on("check_for_updates", async (event) => {
	// Manual update check triggered from renderer
	checkForUpdates();
	event.returnValue = true;
});

ipcMain.on("rotator_set_follow", (event, mode) => {
	rotatorFollowMode = mode;
	if (mode === 'off') {
		rotatorPendingSet  = null;   // discard any queued move
		rotatorPollPending = false;  // no more polls
		rotatorLastCmdAz   = null;   // clear tracked position
		rotatorLastCmdEl   = null;
		rotatorCurrentAz   = null;
		rotatorCurrentEl   = null;
		rotatorStopping    = false;
		rotatorStopAfterRPRT = null;
		// Write S directly — bypasses queue, instant halt regardless of rotatorBusy
		if (rotatorSocket && !rotatorSocket.destroyed) {
			rotatorSocket.write('S\n');
		}
	} else {
		// Connect now so the first P command goes out without a connection delay.
		// Don't send p yet — some backends' p hangs until a P has been sent first.
		const profile = defaultcfg.profiles[defaultcfg.profile ?? 0];
		if ((profile.rotator_host || '').trim()) {
			rotatorEnsureConnected();
		}
	}
	event.returnValue = true;
});

ipcMain.handle("rotator_park", async (event, profile) => {
	// Ensure connection
	const host = (profile.rotator_host || '').trim();
	const port = parseInt(profile.rotator_port, 10);
	if (!host || !port) {
		return { success: false, error: 'Rotator not configured' };
	}

	// Ensure we're using the correct profile
	const currentProfileIndex = defaultcfg.profile ?? 0;
	defaultcfg.profiles[currentProfileIndex] = profile;

	// Helper: send park commands to rotator
	const sendParkCommands = (resolve) => {
		const parkAz = profile.rotator_park_az || 0;
		const parkEl = profile.rotator_park_el || 0;
		rotatorSocket.write('S\n');
		setTimeout(() => {
			sendToRotator(parkAz, parkEl);
			resolve({ success: true });
		}, 500);
	};

	// Ensure connection and wait for it to be established
	return new Promise((resolve) => {
		const target = `${host}:${port}`;

		// Check if already connected to the correct target
		if (rotatorSocket && !rotatorSocket.destroyed && rotatorConnectedTo === target) {
			sendParkCommands(resolve);
			return;
		}

		// Need to establish connection
		if (rotatorConnecting) {
			// Already connecting, wait for it with timeout fallback
			let resolved = false;
			const checkInterval = setInterval(() => {
				if (!rotatorConnecting && rotatorSocket && !rotatorSocket.destroyed && rotatorConnectedTo === target) {
					cleanup();
					sendParkCommands(resolve);
				} else if (!rotatorConnecting && !rotatorSocket) {
					cleanup();
					resolve({ success: false, error: 'Connection failed' });
				}
			}, 100);

			// Timeout fallback: prevent infinite polling
			const timeoutId = setTimeout(() => {
				if (!resolved) {
					cleanup();
					resolve({ success: false, error: 'Connection timeout' });
				}
			}, 10000); // 10 second timeout for connection waiting

			const cleanup = () => {
				if (resolved) return;
				resolved = true;
				clearInterval(checkInterval);
				clearTimeout(timeoutId);
			};
			return;
		}

		// Initiate connection using shared handler
		rotatorCreateConnection(host, port, {
			onConnect: (client) => sendParkCommands(resolve),
			onError: (err) => resolve({ success: false, error: err.message }),
			onClose: () => resolve({ success: false, error: 'Connection closed' })
		});
	});
});

ipcMain.on("restart_udp", async (event) => {
	// Restart UDP server with current configuration
	startUdpServer();
	event.returnValue = true;
});

ipcMain.on("get_udp_status", async (event) => {
	// Return current UDP server status (global settings)
	event.returnValue = {
		enabled: defaultcfg.udp_enabled !== undefined ? defaultcfg.udp_enabled : true,
		port: defaultcfg.udp_port || 2333,
		running: udpServer !== null
	};
});

// Dynamic Profile System IPC Handlers

ipcMain.on("create_profile", async (event, name) => {
	let data = storage.getSync('basic');

	const newProfile = {
		wavelog_url: data.profiles[data.profile || 0].wavelog_url || '',
		wavelog_key: data.profiles[data.profile || 0].wavelog_key || '',
		wavelog_id: data.profiles[data.profile || 0].wavelog_id || '0',
		wavelog_radioname: 'WLGate',
		wavelog_pmode: true,
		flrig_host: '127.0.0.1',
		flrig_port: '12345',
		flrig_ena: false,
		hamlib_host: '127.0.0.1',
		hamlib_port: '4532',
		hamlib_ena: false,
		ignore_pwr: false,
		rotator_host: '',
		rotator_port: '4533',
		rotator_threshold_az: 2,
		rotator_threshold_el: 2,
		rotator_park_az: 0,
		rotator_park_el: 0,
	};

	data.profiles.push(newProfile);
	data.profileNames.push(name || `Profile ${data.profiles.length}`);

	storage.setSync('basic', data);

	event.returnValue = { success: true, index: data.profiles.length - 1 };
});

ipcMain.on("delete_profile", async (event, index) => {
	let data = storage.getSync('basic');

	// Prevent deleting if only 2 profiles remain
	if (data.profiles.length <= 2) {
		event.returnValue = { success: false, error: 'Minimum 2 profiles required' };
		return;
	}

	// Prevent deleting active profile
	if ((data.profile || 0) === index) {
		event.returnValue = { success: false, error: 'Cannot delete active profile' };
		return;
	}

	data.profiles.splice(index, 1);
	data.profileNames.splice(index, 1);

	// Adjust active index if needed
	if ((data.profile || 0) > index) {
		data.profile = (data.profile || 0) - 1;
	}

	storage.setSync('basic', data);

	event.returnValue = { success: true };
});

ipcMain.on("rename_profile", async (event, index, newName) => {
	let data = storage.getSync('basic');
	data.profileNames[index] = newName;

	storage.setSync('basic', data);

	event.returnValue = { success: true };
});

ipcMain.on("switch_profile", async (event, index) => {
	let data = storage.getSync('basic');
	data.profile = index;

	storage.setSync('basic', data);

	event.returnValue = { success: true };
});

function cleanupConnections() {
    console.log('Cleaning up active TCP connections...');

    // Close all tracked TCP connections
    activeConnections.forEach(connection => {
        try {
            if (connection && !connection.destroyed) {
                connection.destroy();
                console.log('Closed TCP connection');
            }
        } catch (error) {
            console.error('Error closing TCP connection:', error);
        }
    });

    // Clear the connections set
    activeConnections.clear();
    console.log('All TCP connections cleaned up');

    // Abort all in-flight HTTP requests
    activeHttpRequests.forEach(request => {
        try {
            request.abort();
            console.log('Aborted HTTP request');
        } catch (error) {
            console.error('Error aborting HTTP request:', error);
        }
    });

    // Clear the HTTP requests set
    activeHttpRequests.clear();
    console.log('All HTTP requests aborted');
}

function shutdownApplication() {
    if (isShuttingDown) {
        console.log('Shutdown already in progress, ignoring duplicate request');
        return;
    }

    isShuttingDown = true;
    console.log('Initiating application shutdown...');

    try {
        // Signal renderer to clear timers and connections
        if (s_mainWindow && !s_mainWindow.isDestroyed()) {
            console.log('Sending cleanup signal to renderer...');
            s_mainWindow.webContents.send('cleanup');
        }

        // Clean up rotator poll and socket
        if (rotatorPollTimer) { clearInterval(rotatorPollTimer); rotatorPollTimer = null; }
        closeRotatorSocket();

        // Clean up TCP connections
        cleanupConnections();

        // Close all servers
        if (udpServer) {
            console.log('Closing UDP server...');
            try {
                udpServer.close();
            } catch (e) {
                console.error('Error closing UDP server:', e);
            }
            udpServer = null;
        }
        if (qsyServer) {
            console.log('Closing QSY server...');
            try {
                qsyServer.close();
            } catch (e) {
                console.error('Error closing QSY server:', e);
            }
            qsyServer = null;
        }
        if (wsServer) {
            console.log('Closing WebSocket server and clients...');
            // Close all WebSocket client connections with explicit termination
            wsClients.forEach(client => {
                try {
                    if (client.readyState === WebSocket.OPEN || client.readyState === WebSocket.CONNECTING) {
                        client.close(1001, 'Server shutting down');
                    }
                } catch (e) {
                    // Client may already be closed, try terminate
                    try {
                        client.terminate();
                    } catch (terminateError) {
                        // Ignore, client is gone
                    }
                }
            });
            wsClients.clear();
            try {
                wsServer.close();
            } catch (e) {
                console.error('Error closing WebSocket server:', e);
            }
            // wsServer will be set to null by the 'close' event handler
        }
        if (wssServer) {
            console.log('Closing Secure WebSocket server and clients...');
            // Close all Secure WebSocket client connections with explicit termination
            wssClients.forEach(client => {
                try {
                    if (client.readyState === WebSocket.OPEN || client.readyState === WebSocket.CONNECTING) {
                        client.close(1001, 'Server shutting down');
                    }
                } catch (e) {
                    // Client may already be closed, try terminate
                    try {
                        client.terminate();
                    } catch (terminateError) {
                        // Ignore, client is gone
                    }
                }
            });
            wssClients.clear();
            try {
                wssServer.close();
            } catch (e) {
                console.error('Error closing Secure WebSocket server:', e);
            }
            // wssServer will be set to null by the 'close' event handler
        }
        if (wssHttpsServer) {
            console.log('Closing HTTPS server...');
            try {
                wssHttpsServer.close();
            } catch (e) {
                console.error('Error closing HTTPS server:', e);
            }
            // wssHttpsServer will be set to null by the 'close' event handler
        }
    } catch (error) {
        console.error('Error during server shutdown:', error);
    }
}

function show_noti(arg) {
	if (Notification.isSupported()) {
		try {
			const notification = new Notification({
				title: 'Wavelog',
				body: arg
			});
			notification.show();
		} catch(e) {
			console.log("No notification possible on this system / ignoring");
		}
	} else {
		console.log("Notifications are not supported on this platform");
	}
}

ipcMain.on("test", async (event,arg) => {
	
	let result={};
	let plain;
	try {
		plain=await send2wavelog(arg,DemoAdif, true);
	} catch (e) {
		plain=e;
		console.log(plain);
	} finally {
		try {
			result.payload=JSON.parse(plain.resString);
		} catch (ee) {
			result.payload=plain.resString;
		} finally {
			result.statusCode=plain.statusCode;
			event.returnValue=result;
		}
	}
});

app.on('before-quit', () => {
    console.log('before-quit event triggered');
    shutdownApplication();
});

process.on('SIGINT', () => {
    console.log('SIGINT received, initiating shutdown...');
    shutdownApplication();
    process.exit(0);
});

app.on('will-quit', () => {
	try {
		if (!sleepable && powerSaveBlockerId !== undefined) {
			powerSaveBlocker.stop(powerSaveBlockerId);
		}
	} catch(e) {
		console.log(e);
	}
});

if (!gotTheLock) {
	// Another instance is running - signal it to quit and relaunch
	console.log('Another instance is running, requesting it to quit...');
	// Wait for the old instance to quit, then relaunch
	setTimeout(() => {
		app.relaunch();
		app.exit(0);
	}, 1000);
} else {
	// Handle second instance trying to start - quit to let new instance take over
	app.on('second-instance', (event, commandLine, workingDirectory) => {
		console.log('Second instance detected, quitting to let new instance take over...');
		app.quit();
	});

	// Load config from storage before starting servers
	let storedcfg = storage.getSync('basic');
	if (!(storedcfg.wavelog_url) && !(storedcfg.profiles)) {
		// No saved config, use defaults
		defaultcfg.profiles = [defaultcfg];
		defaultcfg.profile = 0;
	} else if (!(storedcfg.profiles)) {
		// Old config without array, convert it
		defaultcfg.profiles = [storedcfg, defaultcfg];
		defaultcfg.profile = 0;
	} else {
		// Use saved config
		defaultcfg = storedcfg;
	}

	// Ensure global UDP settings exist (migration for older configs)
	if (defaultcfg.udp_enabled === undefined) {
		defaultcfg.udp_enabled = true;
	}
	if (defaultcfg.udp_port === undefined) {
		defaultcfg.udp_port = 2333;
	}

	console.log('Loaded config, UDP enabled:', defaultcfg.udp_enabled, 'port:', defaultcfg.udp_port);

	startserver();
	app.whenReady().then(() => {
		if (!sleepable) {
			powerSaveBlockerId = powerSaveBlocker.start('prevent-app-suspension');
		}
		s_mainWindow=createWindow();
		globalShortcut.register('Control+Shift+I', () => { return false; });
		app.on('activate', function () {
			if (BrowserWindow.getAllWindows().length === 0) createWindow()
		});
		s_mainWindow.webContents.once('dom-ready', function() {
			if (msgbacklog.length>0) {
				s_mainWindow.webContents.send('updateMsg',msgbacklog.pop());
			}
			// Check for updates on startup
			checkForUpdates();
		});

		// Show certificate install window if it was pending (before main window was ready)
		if (pendingCertInstall) {
			// Small delay to ensure main window is fully visible
			setTimeout(() => {
				showCertInstallWindow();
			}, 500);
		}
	});
}

app.on('window-all-closed', function () {
	console.log('All windows closed, initiating shutdown...');
	if (!isShuttingDown) {
		shutdownApplication();
	}
	if (process.platform !== 'darwin') app.quit();
	else app.quit();
})

function normalizeTxPwr(adifdata) {
	return adifdata.replace(/<TX_PWR:(\d+)>([^<]+)/gi, (match, length, value) => {
		const cleanValue = value.trim().toLowerCase();
		
		const numMatch = cleanValue.match(/^(\d+(?:\.\d+)?)/);
		if (!numMatch) return match; // not a valid number, return original match
		
		let watts = parseFloat(numMatch[1]);
		
		// get the unit if present
		if (cleanValue.includes('kw')) {
			watts *= 1000;
		} else if (cleanValue.includes('mw')) {
			watts *= 0.001;
		}
		// if it's just 'w' we assume it's already in watts
		// would be equal to
		// } else if (cleanValue.includes('w')) {
		// 	watts *= 1;
		// }
		
		// get the new length and return the new TX_PWR tag
		const newValue = watts.toString();
		return `<TX_PWR:${newValue.length}>${newValue}`;
	});
}

function normalizeKIndex(adifdata) {
	return adifdata.replace(/<K_INDEX:(\d+)>([^<]+)/gi, (match, length, value) => {
		const numValue = parseFloat(value.trim());
		if (isNaN(numValue)) return ''; // Remove if not a number
		
		// Round to nearest integer and clamp to 0-9 range
		let kIndex = Math.round(numValue);
		if (kIndex < 0) kIndex = 0;
		if (kIndex > 9) kIndex = 9;
		
		return `<K_INDEX:${kIndex.toString().length}>${kIndex}`;
	});
}

function manipulateAdifData(adifdata) {
	adifdata = normalizeTxPwr(adifdata);
	adifdata = normalizeKIndex(adifdata);
	// add more manipulation if necessary here
	// ...
	return adifdata;
}

function parseADIF(adifdata) {
	const { ADIF } = require("tcadif");
	const normalizedData = manipulateAdifData(adifdata);
	const adiReader = ADIF.parse(normalizedData);
	return adiReader.toObject();
}

function writeADIF(adifObject) {
	const { ADIF } = require("tcadif");
	const adiWriter = new ADIF(adifObject);
	return adiWriter;
}

function freqToBand(freq_mz) {
	const f = parseFloat(freq_mz);
	if (isNaN(f)) return null;

	const bandMap = require('tcadif/lib/enums/Band');
	for (const [band, { lowerFreq, upperFreq }] of Object.entries(bandMap))
		if (f >= parseFloat(lowerFreq) && f <= parseFloat(upperFreq))
			return band;

	return null;
}

function send2wavelog(o_cfg,adif, dryrun = false) {
	let clpayload={};
	clpayload.key=o_cfg.wavelog_key.trim();
	clpayload.station_profile_id=o_cfg.wavelog_id.trim();
	clpayload.type='adif';
	clpayload.string=adif;
	const postData=JSON.stringify(clpayload);
	let httpmod='http';
	if (o_cfg.wavelog_url.toLowerCase().startsWith('https')) {
		httpmod='https';
	}
	const https = require(httpmod);
	const options = {
		method: 'POST',
		timeout: 5000,
		rejectUnauthorized: false,
		headers: {
			'Content-Type': 'application/json',
			'User-Agent': 'SW2WL_v' + app.getVersion(),
			'Content-Length': postData.length
		}
	};

	return new Promise((resolve, reject) => {
		let rej=false;
		let result={};
		let url=o_cfg.wavelog_url + '/api/qso';
		if (dryrun) { url+='/true'; }
		const req = https.request(url,options, (res) => {

			result.statusCode=res.statusCode;
			if (res.statusCode < 200 || res.statusCode > 299) {
				rej=true;
			}

			const body = [];
			res.on('data', (chunk) => body.push(chunk));
			res.on('end', () => {
				// Remove request from tracking when completed
				activeHttpRequests.delete(req);

				let resString = Buffer.concat(body).toString();
				if (rej) {
					if (resString.indexOf('html>')>0) {
						resString='{"status":"failed","reason":"wrong URL"}';
					}
					result.resString=resString;
					reject(result);
				} else {
					result.resString=resString;
					resolve(result);
				}
			})
		})

		req.on('error', (err) => {
			// Remove request from tracking on error
			activeHttpRequests.delete(req);
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"internet problem"}';
			reject(result);
		})

		req.on('timeout', (err) => {
			// Remove request from tracking on timeout
			activeHttpRequests.delete(req);
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"timeout"}';
			reject(result);
		})

		// Track the HTTP request for cleanup
		activeHttpRequests.add(req);

		req.write(postData);
		req.end();
	});

}

// Function to start UDP server with configured settings
function startUdpServer() {
	// Close existing server if running
	if (udpServer) {
		try {
			udpServer.close();
		} catch (e) {
			console.log('Error closing existing UDP server:', e);
		}
		udpServer = null;
	}

	const udpEnabled = defaultcfg.udp_enabled !== undefined ? defaultcfg.udp_enabled : true;
	const udpPort = defaultcfg.udp_port || 2333;

	if (!udpEnabled) {
		console.log('UDP listener disabled');
		tomsg('UDP Listener disabled');
		return;
	}

	console.log('Starting UDP server on port ' + udpPort);

	udpServer = udp.createSocket('udp4');

	udpServer.on('error', function(err) {
		console.error('UDP server error:', err);
		if (err.code === 'EADDRINUSE') {
			tomsg('Port ' + udpPort + ' already in use. Stop the other application and restart.');
		} else {
			tomsg('UDP server error: ' + err.message);
		}
	});

	udpServer.on('listening', function() {
		console.log('UDP server is listening on port ' + udpPort);
	});

	udpServer.on('message',async function(msg,info){
		let parsedXML={};
		let adobject={};
		if (msg.toString().includes("xml")) {	// detect if incoming String is XML
			try {
				xml.parseString(msg.toString(), function (err,dat) {
					parsedXML=dat;
				});
				let qsodatum = new Date(Date.parse(parsedXML.contactinfo.timestamp[0]+"Z")); // Added Z to make it UTC
				const qsodat=fmt(qsodatum);
				if (parsedXML.contactinfo.mode[0] == 'USB' || parsedXML.contactinfo.mode[0] == 'LSB') {	 // TCADIF lib is not capable of using USB/LSB
					parsedXML.contactinfo.mode[0]='SSB';
				}
				adobject = { qsos: [
					{
						CALL: parsedXML.contactinfo.call[0],
						MODE: parsedXML.contactinfo.mode[0],
						QSO_DATE_OFF: qsodat.d,
						QSO_DATE: qsodat.d,
						TIME_OFF: qsodat.t,
						TIME_ON: qsodat.t,
						RST_RCVD: parsedXML.contactinfo.rcv[0],
						RST_SENT: parsedXML.contactinfo.snt[0],
						FREQ: ((1*parseInt(parsedXML.contactinfo.txfreq[0]))/100000).toString(),
						FREQ_RX: ((1*parseInt(parsedXML.contactinfo.rxfreq[0]))/100000).toString(),
						OPERATOR: parsedXML.contactinfo.operator[0],
						COMMENT: parsedXML.contactinfo.comment[0],
						POWER: parsedXML.contactinfo.power[0],
						STX: parsedXML.contactinfo.sntnr[0],
						RTX: parsedXML.contactinfo.rcvnr[0],
						MYCALL: parsedXML.contactinfo.mycall[0],
						GRIDSQUARE: parsedXML.contactinfo.gridsquare[0],
						STATION_CALLSIGN: parsedXML.contactinfo.mycall[0]
					} ]};
				let band = freqToBand(adobject.qsos[0].FREQ);
				if (band) adobject.qsos[0].BAND = band;
			} catch (e) {}
		} else {
			try {
				adobject=parseADIF(msg.toString());
			} catch(e) {
				tomsg('<div class="alert alert-danger" role="alert">Received broken ADIF</div>');
				return;
			}
		}
		let plainret='';
		if (adobject.qsos.length>0) {
			let x={};
			try {
				const outadif=writeADIF(adobject);
				plainret=await send2wavelog(defaultcfg.profiles[defaultcfg.profile ?? 0],outadif.stringify());
				x.state=plainret.statusCode;
				x.payload = JSON.parse(plainret.resString);
			} catch(e) {
				try {
					x.payload=JSON.parse(e.resString);
				} catch (ee) {
					x.state=e.statusCode;
					x.payload={};
					x.payload.string=e.resString;
					x.payload.status='bug';
				} finally {
					x.payload.status='bug';
				}
			}
			if (x.payload.status == 'created') {
				adobject.created=true;
				show_noti("QSO added: "+adobject.qsos[0].CALL);
			} else {
				adobject.created=false;
				console.log(x);
				adobject.fail=x;
				if (x.payload.messages) {
					adobject.fail.payload.reason=x.payload.messages.join();
				}
				show_noti("QSO NOT added: "+adobject.qsos[0].CALL);
			}
			s_mainWindow.webContents.send('updateTX', adobject);
			tomsg('');
		} else {
			tomsg('<div class="alert alert-danger" role="alert">No ADIF detected. WSJT-X: Use ONLY Secondary UDP-Server</div>');
		}
	});

	udpServer.bind(udpPort);
	console.log('UDP server started on port '+udpPort);
	tomsg('Waiting for QSO / Listening on UDP '+udpPort);
}

function tomsg(msg) {
	try {
		s_mainWindow.webContents.send('updateMsg',msg);
	} catch(e) {
		msgbacklog.push(msg);
	}
}

// Generate or load self-signed certificate for HTTPS server
function setupCertificates() {
	const userDataPath = app.getPath('userData');
	const certDir = path.join(userDataPath, 'certs');

	// Check if certificates already exist
	const keyPath = path.join(certDir, 'server.key');
	const certPath = path.join(certDir, 'server.crt');

	if (fs.existsSync(keyPath) && fs.existsSync(certPath)) {
		// Load existing certificates
		certPaths = {
			key: fs.readFileSync(keyPath),
			cert: fs.readFileSync(certPath)
		};
		console.log('Using existing SSL certificates');
		return { success: true, newlyGenerated: false };
	}

	// Generate new certificates
	try {
		// Create cert directory if it doesn't exist
		if (!fs.existsSync(certDir)) {
			fs.mkdirSync(certDir, { recursive: true });
		}

		// Generate RSA key pair
		const keys = forge.pki.rsa.generateKeyPair(2048);

		// Create certificate
		const cert = forge.pki.createCertificate();
		cert.publicKey = keys.publicKey;
		cert.serialNumber = '01';
		cert.validity.notBefore = new Date();
		cert.validity.notAfter = new Date();
		cert.validity.notAfter.setFullYear(cert.validity.notBefore.getFullYear() + 10); // 10 years

		// Set subject and issuer (self-signed)
		const attrs = [{
			name: 'commonName',
			value: '127.0.0.1'
		}];
		cert.setSubject(attrs);
		cert.setIssuer(attrs);

		// Add extensions including SANs
		cert.setExtensions([{
			name: 'basicConstraints',
			cA: false
		}, {
			name: 'keyUsage',
			digitalSignature: true,
			keyEncipherment: true
		}, {
			name: 'extKeyUsage',
			serverAuth: true,
			clientAuth: true
		}, {
			name: 'subjectAltName',
			altNames: [{
				type: 7, // IP address
				ip: '127.0.0.1'
			}, {
				type: 2, // DNS name
				value: 'localhost'
			}, {
				type: 7, // IPv6 address
				ip: '::1'
			}]
		}]);

		// Self-sign the certificate
		cert.sign(keys.privateKey, forge.md.sha256.create());
		
		// Convert to PEM format
		const certPem = forge.pki.certificateToPem(cert);
		const keyPem = forge.pki.privateKeyToPem(keys.privateKey);

		// Save certificates
		fs.writeFileSync(keyPath, keyPem);
		fs.writeFileSync(certPath, certPem);

		certPaths = {
			key: keyPem,
			cert: certPem
		};

		console.log('Generated new SSL certificates');
		return { success: true, newlyGenerated: true };
	} catch (error) {
		console.error('Failed to generate certificates:', error);
		tomsg('Warning: Failed to generate SSL certificates. HTTPS server will not be available.');
		return { success: false, newlyGenerated: false };
	}
}

// Get certificate for user installation
function getCaCertificate() {
	const userDataPath = app.getPath('userData');
	const certPath = path.join(userDataPath, 'certs', 'server.crt');

	if (fs.existsSync(certPath)) {
		return fs.readFileSync(certPath, 'utf8');
	}
	return null;
}

// Check if certificate is installed in system trust store
function isCertificateInstalled() {
	const { execSync } = require('child_process');
	const userDataPath = app.getPath('userData');
	const certPath = path.join(userDataPath, 'certs', 'server.crt');

	if (!fs.existsSync(certPath)) {
		return false;
	}

	const platform = process.platform;

	try {
		if (platform === 'win32') {
			// Windows: Check if cert exists in Root store
			// Use certutil to dump the Root store and search for our cert
			try {
				const output = execSync('certutil -store Root', { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] });
				// Check for our certificate's common name (127.0.0.1)
				return output.includes('CN=127.0.0.1') || output.includes('127.0.0.1');
			} catch (err) {
				console.log('Failed to check Windows certificate store:', err.message);
				return false;
			}
		} else if (platform === 'darwin') {
			// macOS: Check if cert exists in System keychain
			try {
				execSync('security find-certificate -c "127.0.0.1" -p /Library/Keychains/System.keychain', { stdio: 'ignore' });
				return true;
			} catch (err) {
				// Also check user keychain as fallback
				try {
					execSync('security find-certificate -c "127.0.0.1" -p ~/Library/Keychains/login.keychain-db', { stdio: 'ignore' });
					return true;
				} catch (userErr) {
					return false;
				}
			}
		} else if (platform === 'linux') {
			// Linux: Check if cert exists in system ca-certificates directories
			const certContent = fs.readFileSync(certPath, 'utf8');
			// Calculate a simple hash of the certificate content
			const crypto = require('crypto');
			const certHash = crypto.createHash('sha256').update(certContent).digest('hex');

			// Check Debian/Ubuntu location
			const debPath = '/usr/local/share/ca-certificates/waveloggate.crt';
			if (fs.existsSync(debPath)) {
				const existingContent = fs.readFileSync(debPath, 'utf8');
				const existingHash = crypto.createHash('sha256').update(existingContent).digest('hex');
				if (certHash === existingHash) return true;
			}

			// Check Fedora/RHEL location
			const rhelPath = '/etc/pki/ca-trust/source/anchors/waveloggate.crt';
			if (fs.existsSync(rhelPath)) {
				const existingContent = fs.readFileSync(rhelPath, 'utf8');
				const existingHash = crypto.createHash('sha256').update(existingContent).digest('hex');
				if (certHash === existingHash) return true;
			}

			// Check Arch Linux location
			const archPath = '/etc/ca-certificates/trust-source/anchors/waveloggate.crt';
			if (fs.existsSync(archPath)) {
				const existingContent = fs.readFileSync(archPath, 'utf8');
				const existingHash = crypto.createHash('sha256').update(existingContent).digest('hex');
				if (certHash === existingHash) return true;
			}

			return false;
		}

		// Unknown platform - assume not installed
		return false;
	} catch (error) {
		console.log('Error checking certificate installation:', error.message);
		return false;
	}
}

// Install certificate in system trust store
async function installCertificate() {
	const { execSync } = require('child_process');
	const userDataPath = app.getPath('userData');
	const certPath = path.join(userDataPath, 'certs', 'server.crt');

	if (!fs.existsSync(certPath)) {
		return {
			success: false,
			message: 'Certificate not found. Please restart the application to generate it.',
			manual: false
		};
	}

	const platform = process.platform;

	try {
		if (platform === 'darwin') {
			// macOS - Use AppleScript to run with admin privileges (shows native password dialog)
			try {
				// Escape the certificate path for shell and AppleScript
				const escapedCertPath = certPath.replace(/'/g, "'\\''");

				// Use AppleScript to execute with administrator privileges
				// This shows the native macOS authentication dialog
				const appleScript = `
					do shell script "security add-trusted-cert -d -p ssl -p basic -k /Library/Keychains/System.keychain '${escapedCertPath}'" with administrator privileges
				`;

				execSync(`osascript -e '${appleScript.replace(/'/g, "'\\''")}'`, { stdio: 'ignore' });
				console.log('Certificate installed in System keychain via AppleScript');
				return {
					success: true,
					message: 'Certificate installed in System keychain. Chrome and Safari should now trust it after restart.',
					manual: false
				};
			} catch (sysError) {
				console.log('AppleScript installation failed:', sysError.message);
				return {
					success: false,
					message: `Installation was cancelled or failed.\n\nPlease try again and enter your macOS password when prompted.\n\nIf you prefer manual installation, run this command in Terminal:\n\nsudo security add-trusted-cert -d -p ssl -p basic -k /Library/Keychains/System.keychain "${certPath}"`,
					manual: true,
					command: `sudo security add-trusted-cert -d -p ssl -p basic -k /Library/Keychains/System.keychain "${certPath}"`
				};
			}
		} else if (platform === 'win32') {
			// Windows - try to install with elevation prompt
			try {
				// Try direct install first (if already running as admin)
				execSync(`certutil -addstore -f Root "${certPath}"`, { stdio: 'ignore' });
				console.log('Certificate installed in Windows trust store');
				return {
					success: true,
					message: 'Certificate installed in Windows trust store.',
					manual: false
				};
			} catch (winError) {
				// Not running as admin - try PowerShell elevation
				try {
					const psScript = `Start-Process powershell -ArgumentList '-Command', 'certutil -addstore -f Root \\"${certPath}\\"' -Verb RunAs`;
					execSync(`powershell -Command "${psScript}"`, { stdio: 'ignore' });
					// Give it a moment for UAC dialog
					await new Promise(resolve => setTimeout(resolve, 2000));
					return {
						success: true,
						message: 'Certificate installation prompt shown. Please approve the UAC prompt and restart your browser.',
						manual: false
					};
				} catch (elevateError) {
					return {
						success: false,
						message: `Installation requires Administrator privileges. Please run PowerShell as Administrator and execute:\n\ncertutil -addstore -f Root "${certPath}"`,
						manual: true,
						command: `certutil -addstore -f Root "${certPath}"`
					};
				}
			}
		} else if (platform === 'linux') {
			// Linux - try pkexec for GUI systems, fall back to manual instructions
			try {
				// Try pkexec (polkit) for GUI password prompt
				const distroInfo = getLinuxDistro();

				// Try Debian/Ubuntu approach first
				if (fs.existsSync('/usr/local/share/ca-certificates/')) {
					const installScript = `cp "${certPath}" /usr/local/share/ca-certificates/waveloggate.crt && update-ca-certificates`;
					execSync(`pkexec sh -c '${installScript}'`, { stdio: 'ignore' });
					return {
						success: true,
						message: 'Certificate installed. Please restart your browser.',
						manual: false
					};
				}
				// Try Fedora/RHEL approach
				else if (fs.existsSync('/etc/pki/ca-trust/source/anchors/')) {
					const installScript = `cp "${certPath}" /etc/pki/ca-trust/source/anchors/waveloggate.crt && update-ca-trust`;
					execSync(`pkexec sh -c '${installScript}'`, { stdio: 'ignore' });
					return {
						success: true,
						message: 'Certificate installed. Please restart your browser.',
						manual: false
					};
				}
				// Try Arch Linux approach
				else if (fs.existsSync('/etc/ca-certificates/trust-source/anchors/')) {
					const installScript = `cp "${certPath}" /etc/ca-certificates/trust-source/anchors/waveloggate.crt && update-ca-trust`;
					execSync(`pkexec sh -c '${installScript}'`, { stdio: 'ignore' });
					return {
						success: true,
						message: 'Certificate installed. Please restart your browser.',
						manual: false
					};
				} else {
					throw new Error('Unknown certificate location');
				}
			} catch (linuxError) {
				// Fall back to manual instructions
				return {
					success: false,
					message: `Automatic installation failed. Please run these commands in Terminal:\n\nDebian/Ubuntu:\nsudo cp "${certPath}" /usr/local/share/ca-certificates/waveloggate.crt\nsudo update-ca-certificates\n\nFedora/RHEL:\nsudo cp "${certPath}" /etc/pki/ca-trust/source/anchors/waveloggate.crt\nsudo update-ca-trust\n\nArch Linux:\nsudo cp "${certPath}" /etc/ca-certificates/trust-source/anchors/waveloggate.crt\nsudo update-ca-trust`,
					manual: true
				};
			}
		}

		return {
			success: false,
			message: 'Unsupported platform for automatic certificate installation.',
			manual: true
		};
	} catch (error) {
		console.error('Certificate installation error:', error);
		return {
			success: false,
			message: `Installation failed: ${error.message}`,
			manual: true
		};
	}
}

function getLinuxDistro() {
	try {
		const { execSync } = require('child_process');
		if (fs.existsSync('/etc/os-release')) {
			const osRelease = fs.readFileSync('/etc/os-release', 'utf8');
			const match = osRelease.match(/ID=([^\n]+)/);
			if (match) return match[1];
		}
	} catch (e) {
		// Ignore
	}
	return 'unknown';
}

// Request handler for both HTTP and HTTPS servers
function createRequestHandler(req, res) {
	// Handle CORS preflight requests (OPTIONS)
	if (req.method === 'OPTIONS') {
		res.setHeader('Access-Control-Allow-Origin', '*');
		res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
		res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization, X-Requested-With');
		res.setHeader('Access-Control-Max-Age', '86400');
		res.writeHead(204);
		res.end();
		return;
	}

	// Handle QSY requests
	res.setHeader('Access-Control-Allow-Origin', '*');
	res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
	res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization, X-Requested-With');

	const parts = req.url.substr(1).split('/');
	const qrg = parts[0];
	const mode = parts[1] || '';

	if (Number.isInteger(Number.parseInt(qrg))) {
		settrx(qrg,mode);
		res.writeHead(200, {'Content-Type': 'text/plain'});
		res.end('OK');
	} else {
		res.writeHead(400, {'Content-Type': 'text/plain'});
		res.end('Invalid frequency');
	}
}

function showCertInstallWindow() {
	// Close existing window if open
	if (certInstallWindow && !certInstallWindow.isDestroyed()) {
		certInstallWindow.focus();
		return;
	}

	// If main window is not ready yet, mark as pending and return
	// The main window ready handler will call this again
	if (!s_mainWindow || s_mainWindow.isDestroyed()) {
		pendingCertInstall = true;
		console.log('Main window not ready, cert install will show after window is ready');
		return;
	}

	pendingCertInstall = false;

	// Determine if we should attach to parent (only if main window is visible and ready)
	const useParent = s_mainWindow && !s_mainWindow.isDestroyed() && s_mainWindow.isVisible();

	certInstallWindow = new BrowserWindow({
		width: 600,
		height: 500,
		resizable: false,
		...(useParent ? { parent: s_mainWindow, modal: true } : {}),
		autoHideMenuBar: app.isPackaged,
		webPreferences: {
			contextIsolation: false,
			nodeIntegration: true,
			devTools: !app.isPackaged,
			enableRemoteModule: true,
			preload: path.join(__dirname, 'preload.js')
		}
	});

	if (app.isPackaged) {
		certInstallWindow.setMenu(null);
	}

	certInstallWindow.loadFile('cert-install.html');
	certInstallWindow.setTitle('WaveLogGate - SSL Certificate Installation');

	certInstallWindow.on('closed', () => {
		certInstallWindow = null;
	});
}

function startserver() {
	try {
		// Setup SSL certificates
		const certResult = setupCertificates();

		// Start UDP server (will check config)
		startUdpServer();

		// Prompt for certificate installation if:
		// 1. Cert was just generated, OR
		// 2. Cert exists but is NOT installed in system trust store
		if (certResult.success) {
			const certInstalled = isCertificateInstalled();

			if (certResult.newlyGenerated || !certInstalled) {
				console.log('Certificate installation prompt needed - newlyGenerated:', certResult.newlyGenerated, 'installed:', certInstalled);
				// Schedule prompt after window is ready
				setTimeout(() => {
					showCertInstallWindow();
				}, 2000);
			} else {
				console.log('Certificate is installed in system trust store');
			}
		}

		// Create dual-mode HTTP/HTTPS server on port 54321
		if (certResult.success && certPaths.key && certPaths.cert) {
			// Create httpolyglot server (handles both HTTP and HTTPS on same port)
			const serverOptions = {
				key: certPaths.key,
				cert: certPaths.cert,
				minVersion: 'TLSv1.2'
			};

			qsyServer = httpolyglot.createServer(serverOptions, createRequestHandler);
			qsyServer.on('error', (err) => {
				if (err.code === 'EADDRINUSE') {
					console.error('QSY server port 54321 already in use');
				} else {
					console.error('QSY server error:', err);
				}
			});
			qsyServer.listen(54321, () => {
				console.log('Dual-mode HTTP/HTTPS QSY server listening on port 54321');
			});
		} else {
			// Fallback to HTTP-only if certificates are not available
			qsyServer = http.createServer(createRequestHandler);
			qsyServer.on('error', (err) => {
				if (err.code === 'EADDRINUSE') {
					console.error('QSY server port 54321 already in use');
				} else {
					console.error('QSY server error:', err);
				}
			});
			qsyServer.listen(54321, () => {
				console.log('HTTP-only QSY server listening on port 54321');
			});
		}

		// Start WebSocket server
		startWebSocketServer();

		// Start Secure WebSocket server
		startSecureWebSocketServer();

		// Start rotator position polling (every 2 s; no-ops when no rotator configured)
		startRotatorPoll();

	} catch(e) {
		console.error('Error in startserver:', e);
		tomsg('Some other Tool blocks Port 2333/54321/54322. Stop it, and restart this');
	}
}

function startWebSocketServer() {
	try {
		wsServer = new WebSocket.Server({
			port: 54322,
			exclusive: true,
			clientTracking: true // Enable built-in client tracking
		});

		wsServer.on('connection', (ws) => {
			wsClients.add(ws);
			console.log('WebSocket client connected');

			// Set up cleanup for this connection
			const cleanupClient = () => {
				wsClients.delete(ws);
				// Ensure socket is fully cleaned up
				if (ws.readyState !== WebSocket.CLOSED) {
					ws.terminate();
				}
			};

			ws.on('close', cleanupClient);

			ws.on('error', (error) => {
				console.error('WebSocket client error:', error);
				cleanupClient();
			});

			// Handle unexpected termination
			ws.on('unexpected-response', (req, res) => {
				console.error('WebSocket unexpected response:', res.statusCode);
				cleanupClient();
			});

			ws.on('message', handleWsIncomingMessage);

			// Send current radio status on connection
			try {
				ws.send(JSON.stringify({
					type: 'welcome',
					message: 'Connected to WaveLogGate WebSocket server'
				}));
				broadcastRadioStatus(currentCAT);
			} catch (sendError) {
				console.error('Error sending welcome message:', sendError);
				cleanupClient();
			}
		});

		wsServer.on('error', (error) => {
			console.error('WebSocket server error:', error);
			// If server fails critically, nullify it so shutdown knows it's gone
			if (error.code === 'EADDRINUSE') {
				console.error('Port 54322 already in use, WebSocket server not started');
				wsServer = null;
			}
		});

		wsServer.on('close', () => {
			console.log('WebSocket server closed');
			wsServer = null;
		});

		console.log('WebSocket server started on port 54322');
	} catch(e) {
		console.error('WebSocket server startup error:', e);
		wsServer = null;
	}
}

function startSecureWebSocketServer() {
	if (!certPaths.key || !certPaths.cert) {
		console.log('No SSL certificates available, skipping secure WebSocket server');
		return;
	}

	try {
		// Create HTTPS server first
		wssHttpsServer = https.createServer({
			key: certPaths.key,
			cert: certPaths.cert
		});

		// Handle HTTPS server errors
		wssHttpsServer.on('error', (error) => {
			console.error('HTTPS server error:', error);
			if (error.code === 'EADDRINUSE') {
				console.error('Port 54323 already in use, secure WebSocket server not started');
				wssHttpsServer = null;
				wssServer = null;
			}
		});

		wssHttpsServer.on('close', () => {
			console.log('HTTPS server closed');
			wssHttpsServer = null;
		});

		// Listen on port 54323 with callback
		wssHttpsServer.listen(54323, () => {
			console.log('HTTPS server listening on port 54323');

			// Attach WebSocket server to the HTTPS server
			wssServer = new WebSocket.Server({ server: wssHttpsServer, clientTracking: true });

			wssServer.on('connection', (ws) => {
				wssClients.add(ws);
				console.log('Secure WebSocket client connected');

				// Set up cleanup for this connection
				const cleanupClient = () => {
					wssClients.delete(ws);
					// Ensure socket is fully cleaned up
					if (ws.readyState !== WebSocket.CLOSED) {
						ws.terminate();
					}
				};

				ws.on('close', cleanupClient);

				ws.on('error', (error) => {
					console.error('Secure WebSocket client error:', error);
					cleanupClient();
				});

				// Handle unexpected termination
				ws.on('unexpected-response', (req, res) => {
					console.error('Secure WebSocket unexpected response:', res.statusCode);
					cleanupClient();
				});

				ws.on('message', handleWsIncomingMessage);

				// Send current radio status on connection
				try {
					ws.send(JSON.stringify({
						type: 'welcome',
						message: 'Connected to WaveLogGate Secure WebSocket server'
					}));
					broadcastRadioStatus(currentCAT);
				} catch (sendError) {
					console.error('Error sending secure welcome message:', sendError);
					cleanupClient();
				}
			});

			wssServer.on('error', (error) => {
				console.error('Secure WebSocket server error:', error);
				// If the WebSocket server fails, we need to clean up the HTTPS server too
				if (wssHttpsServer) {
					wssHttpsServer.close();
					wssHttpsServer = null;
				}
				wssServer = null;
			});

			wssServer.on('close', () => {
				console.log('Secure WebSocket server closed');
				wssServer = null;
			});

			console.log('Secure WebSocket server started on port 54323');
		});

	} catch(e) {
		console.error('Secure WebSocket server startup error:', e);
		// Clean up any partially created resources
		if (wssHttpsServer) {
			try {
				wssHttpsServer.close();
			} catch (closeError) {
				// Ignore errors during cleanup
			}
			wssHttpsServer = null;
		}
		wssServer = null;
	}
}

// ---------------------------------------------------------------------------
// Rotator command queue.
// One persistent TCP connection; commands serialised so responses don't mix.
// P az el\n  →  RPRT 0\n          (position command, RPRT arrives fast)
// p\n        →  az\nel\n           (poll position, NO RPRT on some backends)
// S\n        →  RPRT 0\n          (stop/halt command)
//
// Direction changes: When new target differs from last commanded target,
// S is sent first, then P after S's RPRT arrives (stop-before-move pattern).
// ---------------------------------------------------------------------------

function closeRotatorSocket() {
	if (rotatorBusyTimer) { clearTimeout(rotatorBusyTimer); rotatorBusyTimer = null; }
	if (rotatorSocket) { rotatorSocket.destroy(); rotatorSocket = null; }
	rotatorConnecting  = false;
	rotatorConnectedTo = null;
	rotatorBusy        = false;
	rotatorBuffer      = '';
	rotatorCurrentCmd  = null;
	rotatorHasSentP    = false;
	rotatorLastCmdAz   = null;
	rotatorLastCmdEl   = null;
	rotatorCurrentAz   = null;
	rotatorCurrentEl   = null;
	rotatorStopping    = false;
	rotatorStopAfterRPRT = null;
	if (s_mainWindow && !s_mainWindow.isDestroyed()) {
		s_mainWindow.webContents.send('rotator_update', { connected: false });
	}
}

// Set busy state and arm 5-second watchdog to prevent permanent stuck state.
function rotatorSetBusy(cmd) {
	rotatorBusy       = true;
	rotatorCurrentCmd = cmd;
	if (rotatorBusyTimer) clearTimeout(rotatorBusyTimer);
	rotatorBusyTimer = setTimeout(() => {
		rotatorBusy       = false;
		rotatorCurrentCmd = null;
		rotatorBuffer     = '';
		rotatorBusyTimer  = null;
		rotatorQueueProcess();
	}, 5000);
}

function rotatorClearBusy() {
	if (rotatorBusyTimer) { clearTimeout(rotatorBusyTimer); rotatorBusyTimer = null; }
	rotatorBusy       = false;
	rotatorCurrentCmd = null;
	rotatorBuffer     = '';
}

function rotatorQueueProcess() {
	if (rotatorBusy || !rotatorSocket || rotatorSocket.destroyed) {
		return;
	}

	if (rotatorPendingSet) {
		const { az, el } = rotatorPendingSet;

		// Minimum movement threshold: only move if position differs by threshold
		// Skip check if we don't have current position yet (first move always allowed)
		if (rotatorCurrentAz !== null) {
			// Get threshold from profile
			const profile = defaultcfg.profiles[defaultcfg.profile ?? 0];
			const thresholdAz = profile.rotator_threshold_az || 2;
			const thresholdEl = profile.rotator_threshold_el || 2;

			// Handle azimuth wraparound (359° → 1° = 2° difference, not 358°)
			let azDiff = Math.abs(az - rotatorCurrentAz);
			if (azDiff > 180) azDiff = 360 - azDiff;

			const elDiff = el !== 0 ? Math.abs(el - (rotatorCurrentEl || 0)) : 0;
			// For HF mode (el=0), only check azimuth. For SAT mode, check both.
			if (azDiff < thresholdAz && elDiff < thresholdEl) {
				// Position too close, skip movement
				rotatorPendingSet = null;
				return;
			}
		}

		// Direction reversal detection based on actual current position
		// Only send S if we're currently moving in the opposite direction
		let needStop = false;
		if (rotatorCurrentAz !== null && rotatorLastCmdAz !== null && !rotatorStopping) {
			// Calculate direction from current position to last commanded target
			let lastDir = rotatorLastCmdAz - rotatorCurrentAz;
			if (lastDir > 180) lastDir -= 360;
			if (lastDir < -180) lastDir += 360;

			// Calculate direction from current position to new target
			let newDir = az - rotatorCurrentAz;
			if (newDir > 180) newDir -= 360;
			if (newDir < -180) newDir += 360;

			// If directions have opposite signs, we need to stop first
			needStop = (lastDir * newDir < 0);
		}

		if (needStop) {
			// Send S first, then P after RPRT
			rotatorStopping    = true;
			rotatorStopAfterRPRT = { az, el };
			// Don't clear rotatorPendingSet yet — it becomes the P we send after S completes
			rotatorSetBusy('set');  // Use 'set' type for S (same RPRT format)
			rotatorSocket.write('S\n');
			return;  // Will resume after S's RPRT arrives
		}

		// No stop needed — send P directly
		rotatorPendingSet  = null;
		rotatorHasSentP    = true;
		rotatorLastPTime   = Date.now();
		rotatorLastCmdAz   = az;
		rotatorLastCmdEl   = el;
		rotatorSetBusy('set');
		rotatorSocket.write(`P ${az} ${el}\n`);
	} else if (rotatorPollPending) {
		rotatorPollPending = false;
		rotatorSetBusy('get');
		rotatorSocket.write('p\n');
	}
}

function rotatorOnData(chunk) {
	const raw = chunk.toString();
	rotatorBuffer += raw;

	if (rotatorCurrentCmd === 'set') {
		// P response ends with RPRT N\n
		if (/RPRT\s+-?\d+/.test(rotatorBuffer)) {
			// If we just sent S to stop before a direction change, now send the P
			if (rotatorStopping && rotatorStopAfterRPRT) {
				const { az, el } = rotatorStopAfterRPRT;
				rotatorStopping       = false;
				rotatorStopAfterRPRT  = null;
				rotatorPendingSet     = null;  // Clear the pending set since we're about to send it
				rotatorHasSentP       = true;
				rotatorLastPTime      = Date.now();
				rotatorLastCmdAz      = az;
				rotatorLastCmdEl      = el;
				rotatorSetBusy('set');
				rotatorSocket.write(`P ${az} ${el}\n`);
				rotatorBuffer = '';  // Clear buffer after consuming S's RPRT
				return;
			}

			// Suppress any queued poll — let the rotator start moving uninterrupted.
			// The next poll timer cycle (≤2 s) will pick it up naturally.
			rotatorPollPending = false;
			rotatorClearBusy();
			rotatorQueueProcess();
		}
	} else if (rotatorCurrentCmd === 'get') {
		// p response: az\nel\n (no RPRT on some backends) or az\nel\nRPRT 0\n (standard).
		// Consider complete when RPRT is present OR when ≥2 numeric lines found.
		const hasRPRT = /RPRT\s+-?\d+/.test(rotatorBuffer);
		const nums = rotatorBuffer.split('\n')
			.map(l => l.trim())
			.filter(l => l !== '' && !/^RPRT/.test(l))
			.map(l => parseFloat(l))
			.filter(n => !isNaN(n));
		if (hasRPRT || nums.length >= 2) {
			const az = nums[0];
			const el = nums.length >= 2 ? nums[1] : 0;
			if (nums.length >= 2) {
				rotatorCurrentAz = az;
				rotatorCurrentEl = el;
			}
			rotatorClearBusy();
			if (nums.length >= 2 && s_mainWindow && !s_mainWindow.isDestroyed()) {
				s_mainWindow.webContents.send('rotator_position', { az, el });
			}
			rotatorQueueProcess();
		}
	} else {
		// No command in flight — discard unexpected data (e.g. RPRT from a direct S\n write)
		rotatorBuffer = '';
	}
}

// Shared rotator socket connection handler
// Creates and configures a rotctld TCP connection with standard event handlers
function rotatorCreateConnection(host, port, callbacks = {}) {
	const target = `${host}:${port}`;
	const { onConnect, onError, onClose } = callbacks;

	if (rotatorConnecting) return null;
	rotatorConnecting = true;

	const client = net.createConnection({ host, port }, () => {
		rotatorConnecting  = false;
		rotatorSocket      = client;
		rotatorConnectedTo = target;
		client.setTimeout(0);
		if (s_mainWindow && !s_mainWindow.isDestroyed()) {
			s_mainWindow.webContents.send('rotator_update', { connected: true });
		}
		if (onConnect) onConnect(client);
	});

	client.on('data', rotatorOnData);
	client.setTimeout(3000, () => { if (rotatorConnecting) client.destroy(); });

	client.on('error', (err) => {
		closeRotatorSocket();
		if (s_mainWindow && !s_mainWindow.isDestroyed()) {
			s_mainWindow.webContents.send('rotator_update', { connected: false, error: err.message });
		}
		if (onError) onError(err);
	});

	client.on('close', () => {
		if (rotatorBusyTimer) { clearTimeout(rotatorBusyTimer); rotatorBusyTimer = null; }
		if (rotatorSocket === client) { rotatorSocket = null; rotatorConnectedTo = null; }
		rotatorConnecting  = false;
		rotatorBusy        = false;
		rotatorBuffer      = '';
		rotatorCurrentCmd  = null;
		rotatorHasSentP    = false;
		rotatorLastCmdAz   = null;
		rotatorLastCmdEl   = null;
		rotatorCurrentAz   = null;
		rotatorCurrentEl   = null;
		rotatorStopping    = false;
		rotatorStopAfterRPRT = null;
		if (s_mainWindow && !s_mainWindow.isDestroyed()) {
			s_mainWindow.webContents.send('rotator_update', { connected: false });
		}
		if (onClose) onClose();
	});

	return client;
}

function rotatorEnsureConnected() {
	const profile = defaultcfg.profiles[defaultcfg.profile ?? 0];
	const host = (profile.rotator_host || '').trim();
	const port = parseInt(profile.rotator_port, 10);
	if (!host || !port) return;

	const target = `${host}:${port}`;
	if (rotatorSocket && rotatorConnectedTo !== target) closeRotatorSocket();

	if (rotatorSocket && !rotatorSocket.destroyed) {
		rotatorQueueProcess();
		return;
	}

	rotatorCreateConnection(host, port, {
		onConnect: () => rotatorQueueProcess()
	});
}

function sendToRotator(az, el) {
	rotatorPendingSet = { az, el }; // overwrite — only latest target matters
	rotatorPollPending = false;     // cancel any queued poll — movement takes priority

	// Pre-empt an in-flight p poll: write P immediately rather than waiting for the
	// p response. The pending p response (az/el/RPRT) arriving afterwards will be
	// handled by the 'set' branch (RPRT satisfies the detector; numeric lines drain).
	if (rotatorSocket && !rotatorSocket.destroyed && rotatorCurrentCmd === 'get') {
		const { az: pAz, el: pEl } = rotatorPendingSet;
		rotatorPendingSet  = null;
		rotatorHasSentP    = true;
		rotatorLastPTime   = Date.now();
		rotatorLastCmdAz   = pAz;
		rotatorLastCmdEl   = pEl;
		rotatorClearBusy(); // abandon 'get' state (buffer cleared)
		rotatorSetBusy('set');
		rotatorSocket.write(`P ${pAz} ${pEl}\n`);
		return;
	}

	rotatorEnsureConnected();
}

function startRotatorPoll() {
	if (rotatorPollTimer) return;
	rotatorPollTimer = setInterval(() => {
		if (rotatorFollowMode === 'off') return;
		if (!rotatorHasSentP) return;
		const msSinceP = Date.now() - rotatorLastPTime;
		if (msSinceP < 3000) return;
		const profile = defaultcfg.profiles[defaultcfg.profile ?? 0];
		if (!(profile.rotator_host || '').trim()) return;
		if (!rotatorPollPending) {
			rotatorPollPending = true;
			rotatorEnsureConnected();
		}
	}, 2000);
}

function handleWsIncomingMessage(data) {
	try {
		const msg = JSON.parse(data.toString());
		if (msg.type === 'lookup_result' && msg.payload && msg.payload.azimuth !== undefined) {
			const az = msg.payload.azimuth;
			if (s_mainWindow && !s_mainWindow.isDestroyed()) {
				s_mainWindow.webContents.send('rotator_bearing', { type: 'hf', az });
			}
			if (rotatorFollowMode === 'hf') {
				sendToRotator(az, 0);
			}
		} else if (msg.type === 'satellite_position' && msg.data) {
			const az = parseFloat(msg.data.azimuth);
			const el = parseFloat(msg.data.elevation);
			if (s_mainWindow && !s_mainWindow.isDestroyed()) {
				s_mainWindow.webContents.send('rotator_bearing', { type: 'sat', az, el });
			}
			if (rotatorFollowMode === 'sat') {
				sendToRotator(az, el);
			}
		}
	} catch (e) {
		// Not JSON or unknown message type, ignore
	}
}

function broadcastRadioStatus(radioData) {
	if (!radioData) {
		return;
	}
	currentCAT=radioData;
	let message = {
		type: 'radio_status',
		frequency: radioData.frequency ? parseInt(radioData.frequency) : null,
		mode: radioData.mode || null,
		power: radioData.power || null,
		radio: radioData.radio || 'wlstream',
		timestamp: Date.now()
	};
	// Only include frequency_rx if it's not null
	if (radioData.frequency_rx) {
		message.frequency_rx = parseInt(radioData.frequency_rx);
	}

	const messageStr = JSON.stringify(message);
	// Broadcast to regular WebSocket clients
	wsClients.forEach((client) => {
		if (client.readyState === WebSocket.OPEN) {
			client.send(messageStr);
		}
	});
	// Broadcast to secure WebSocket clients
	wssClients.forEach((client) => {
		if (client.readyState === WebSocket.OPEN) {
			client.send(messageStr);
		}
	});
}


async function get_modes() {
	return new Promise((resolve) => {
		// Check which radio type is enabled
		const profile = defaultcfg.profiles[defaultcfg.profile ?? 0];

		if (profile.hamlib_ena) {
			// For Hamlib, send the command directly
			ipcMain.once('get_info_result', (event, modes) => {
				resolve(modes ?? ['CW','LSB','USB']);
			});
			s_mainWindow.webContents.send('get_info', 'rig.get_modes');
		} else if (profile.flrig_ena) {
			// For FLRig, use the existing method
			ipcMain.once('get_info_result', (event, modes) => {
				resolve(modes ?? ['CW','LSB','USB']);
			});
			s_mainWindow.webContents.send('get_info', 'rig.get_modes');
		} else {
			// No radio control enabled, return default modes
			resolve(['CW','LSB','USB']);
		}
	});
}

function getClosestMode(requestedMode, availableModes) {
	if (availableModes.includes(requestedMode)) {	// Check perfect matches
		return requestedMode;
	}

	const modeFallbacks = {
		'CW': ['CW-L', 'CW-R', 'CW', 'LSB', 'USB'],
		'RTTY': ['RTTY', 'RTTY-R'],
	};

	if (modeFallbacks[requestedMode]) {
		for (let variant of modeFallbacks[requestedMode]) {
			if (availableModes.includes(variant)) {
				return variant;
			}
		}
	}

	const found = availableModes.find(mode =>
					  mode.toUpperCase().startsWith(requestedMode.toUpperCase())
					 );
					 if (found) return found;
					 return null;
}

async function settrx(qrg, mode = '') {
	let avail_modes={};
	try {
		avail_modes=await get_modes();
	} catch(e) {
		avail_modes=[];
	}
	let to={};
	to.qrg=qrg;
	if (mode == 'cw') {
		to.mode=getClosestMode(mode,avail_modes);
	} else {
		if ((to.qrg) < 7999000) {
			to.mode='LSB';
		} else {
			to.mode='USB';
		}
	}
	if (defaultcfg.profiles[defaultcfg.profile ?? 0].flrig_ena) {
		let url="http://"+defaultcfg.profiles[defaultcfg.profile ?? 0].flrig_host+':'+defaultcfg.profiles[defaultcfg.profile ?? 0].flrig_port+'/';
		let postData='';
		let options={};
		let x;

		if (defaultcfg.profiles[defaultcfg.profile ?? 0].wavelog_pmode) {
			postData= '<?xml version="1.0"?>';
			postData+='<methodCall><methodName>rig.set_modeA</methodName><params><param><value>' + to.mode + '</value></param></params></methodCall>';
			options = {
				method: 'POST',
				headers: {
					'User-Agent': 'SW2WL_v' + app.getVersion(),
					'Content-Length': postData.length
				}
			};
			x=await httpPost(url,options,postData);
		}

		postData= '<?xml version="1.0"?>';
		postData+='<methodCall><methodName>main.set_frequency</methodName><params><param><value><double>' + to.qrg + '</double></value></param></params></methodCall>';
		options = {
			method: 'POST',
			headers: {
				'User-Agent': 'SW2WL_v' + app.getVersion(),
				'Content-Length': postData.length
			}
		};
		x=await httpPost(url,options,postData);

	}

	if (defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_ena) {
		const client = net.createConnection({ host: defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_host, port: defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_port }, () => {
			client.write("F " + to.qrg + "\n");
			if (defaultcfg.profiles[defaultcfg.profile ?? 0].wavelog_pmode) {
				client.write("M " + to.mode + " 0\n");
			}
			client.end();
		});

		// Track the connection for cleanup
		activeConnections.add(client);

		client.on("error", (err) => {
			activeConnections.delete(client);
		});
		client.on("close", () => {
			activeConnections.delete(client);
		});
	}

	// Broadcast frequency/mode change to WebSocket clients

	return true;
}

function httpPost(url,options,postData) {
	return new Promise((resolve, reject) => {
		let rej=false;
		let result={};
		const req = http.request(url,options, (res) => {
			let body=[];
			res.on('data', (chunk) => body.push(chunk));
			res.on('end', () => {
				const resString = Buffer.concat(body).toString();
				if (rej) {
					reject(resString);
				} else {
					resolve(resString);
				}
			})
		})

		req.on('error', (err) => {
			req.destroy();
			result.resString='Other Problem';
			reject(result.resString);
		})

		req.on('timeout', (err) => {
			req.destroy();
			result.resString='Timeout';
			reject(result.resString);
		})

		req.write(postData);
		req.end();
	});
}

function fmt(spotDate) {
	const retstr={};
	const d=spotDate.getUTCDate().toString();
	const y=spotDate.getUTCFullYear().toString();
	const m=(1+spotDate.getUTCMonth()).toString();
	const h=spotDate.getUTCHours().toString();
	const i=spotDate.getUTCMinutes().toString();
	const s=spotDate.getUTCSeconds().toString();
	retstr.d=y.padStart(4,'0')+m.padStart(2,'0')+d.padStart(2,'0');
	retstr.t=h.padStart(2,'0')+i.padStart(2,'0')+s.padStart(2,'0');
	return retstr;
}
