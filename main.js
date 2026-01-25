const {app, BrowserWindow, globalShortcut, Notification, powerSaveBlocker } = require('electron/main');
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
let msgbacklog=[];
let qsyServer; // Dual-mode HTTP/HTTPS server for QSY
let currentCAT=null;
var WServer;
let wsServer;
let wsClients = new Set();
let wssServer; // Secure WebSocket server
let wssClients = new Set(); // Secure WebSocket clients
let wssHttpsServer; // HTTPS server for secure WebSocket
let isShuttingDown = false;
let activeConnections = new Set(); // Track active TCP connections
let activeHttpRequests = new Set(); // Track active HTTP requests for cancellation

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
}

const storage = require('electron-json-storage');

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

        // Clean up TCP connections
        cleanupConnections();

        // Close all servers
        if (WServer) {
            console.log('Closing UDP server...');
            WServer.close();
        }
        if (qsyServer) {
            console.log('Closing QSY server...');
            qsyServer.close();
        }
        if (wsServer) {
            console.log('Closing WebSocket server and clients...');
            // Close all WebSocket client connections
            wsClients.forEach(client => {
                if (client.readyState === WebSocket.OPEN) {
                    client.close();
                }
            });
            wsClients.clear();
            wsServer.close();
        }
        if (wssServer) {
            // Close all Secure WebSocket client connections
            wssClients.forEach(client => {
                if (client.readyState === WebSocket.OPEN) {
                    client.close();
                }
            });
            wssClients.clear();
            wssServer.close();
            if (wssHttpsServer) {
                wssHttpsServer.close();
            }
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
	app.quit();
} else {
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
		});
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

const ports = [2333]; // Liste der Ports, an die Sie binden möchten

ports.forEach(port => {
	WServer = udp.createSocket('udp4');
	WServer.on('error', function(err) {
		tomsg('Some other Tool blocks Port '+port+'. Stop it, and restart this');
	});

	WServer.on('message',async function(msg,info){
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
	WServer.bind(port);
});

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

	certInstallWindow = new BrowserWindow({
		width: 600,
		height: 500,
		resizable: false,
		parent: s_mainWindow,
		modal: true,
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

		tomsg('Waiting for QSO / Listening on UDP 2333');

		// Prompt for certificate installation if newly generated
		if (certResult.success && certResult.newlyGenerated) {
			// Schedule prompt after window is ready
			setTimeout(() => {
				showCertInstallWindow();
			}, 2000);
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
	} catch(e) {
		tomsg('Some other Tool blocks Port 2333 or 54321. Stop it, and restart this');
	}
}

function startWebSocketServer() {
	try {
		wsServer = new WebSocket.Server({ port: 54322, exclusive: true });

		wsServer.on('connection', (ws) => {
			wsClients.add(ws);
			console.log('WebSocket client connected');

			ws.on('close', () => {
				wsClients.delete(ws);
			});

			ws.on('error', (error) => {
				console.error('WebSocket error:', error);
				wsClients.delete(ws);
			});

			// Send current radio status on connection
			ws.send(JSON.stringify({
				type: 'welcome',
				message: 'Connected to WaveLogGate WebSocket server'
			}));
			broadcastRadioStatus(currentCAT);
		});

		wsServer.on('error', (error) => {
			console.error('WebSocket server error:', error);
		});

	} catch(e) {
		console.error('WebSocket server startup error:', e);
	}
}

function startSecureWebSocketServer() {
	if (!certPaths.key || !certPaths.cert) {
		return;
	}

	try {
		// Create HTTPS server first
		wssHttpsServer = https.createServer({
			key: certPaths.key,
			cert: certPaths.cert
		});

		// Listen on port 54323
		wssHttpsServer.listen(54323);

		// Attach WebSocket server to the HTTPS server
		wssServer = new WebSocket.Server({ server: wssHttpsServer });

		wssServer.on('connection', (ws) => {
			wssClients.add(ws);

			ws.on('close', () => {
				wssClients.delete(ws);
			});

			ws.on('error', (error) => {
				wssClients.delete(ws);
			});

			// Send current radio status on connection
			ws.send(JSON.stringify({
				type: 'welcome',
				message: 'Connected to WaveLogGate Secure WebSocket server'
			}));
			broadcastRadioStatus(currentCAT);
		});

		wssServer.on('error', (error) => {
			// Silent error handling
		});

		wssHttpsServer.on('error', (error) => {
			// Silent error handling
		});

	} catch(e) {
		// Silent error handling
	}
}

function broadcastRadioStatus(radioData) {
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
