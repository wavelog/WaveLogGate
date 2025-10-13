const {app, BrowserWindow, globalShortcut, Notification, powerSaveBlocker } = require('electron/main');
const path = require('node:path');
const {ipcMain} = require('electron')
const http = require('http');
const xml = require("xml2js");
const net = require('net');

const gotTheLock = app.requestSingleInstanceLock();

let powerSaveBlockerId;
let s_mainWindow;
let msgbacklog=[];
let httpServer;
var WServer;

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
		resizable: false,
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

function createAdvancedWindow (mainWindow) {
	let advancedWindow;
	globalShortcut.register('Control+Shift+D', () => {
		if (!advancedWindow || advancedWindow.isDestroyed()) {
			const bounds = mainWindow.getBounds();
			advancedWindow = new BrowserWindow({
				width: 430,
				height: 250,
				resizable: false,
				autoHideMenuBar: app.isPackaged,
				webPreferences: {
					contextIsolation: false,
					nodeIntegration: true,
					devTools: !app.isPackaged,
					enableRemoteModule: true,
				},
				x: bounds.x + bounds.width + 10,
				y: bounds.y,
			});
			if (app.isPackaged) {
				advancedWindow.setMenu(null);
			}
			advancedWindow.loadFile('advanced.html');
			advancedWindow.setTitle(require('./package.json').name + " V" + require('./package.json').version);
		} else {
			advancedWindow.focus();
		}

	});
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
	app.isQuitting = true;
	app.quit();
	event.returnValue=true;
});

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
    console.log('Shutting down servers...');
    if (WServer) {
        WServer.close();
    }
    if (httpServer) {
        httpServer.close();
    }
});

process.on('SIGINT', () => {
    console.log('SIGINT received, closing servers...');
    if (WServer) WServer.close();
    if (httpServer) httpServer.close();
    process.exit(0);
});

app.on('will-quit', () => {
	try {
		powerSaveBlocker.stop(powerSaveBlockerId);
	} catch(e) {
		console.log(e);
	}
});

if (!gotTheLock) {
	app.quit();
} else {
	startserver();
	app.whenReady().then(() => {
		powerSaveBlockerId = powerSaveBlocker.start('prevent-app-suspension');
		s_mainWindow=createWindow();
		createAdvancedWindow(s_mainWindow);
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
	if (process.platform !== 'darwin') app.quit();
	app.quit();
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

function manipulateAdifData(adifdata) {
	adifdata = normalizeTxPwr(adifdata);
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
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"internet problem"}';
			reject(result);
		})

		req.on('timeout', (err) => {
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"timeout"}';
			reject(result);
		})

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
			tomsg('<div class="alert alert-danger" role="alert">Set ONLY Secondary UDP-Server to Port 2333 at WSJT-X</div>');
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

function startserver() {
	try {
		tomsg('Waiting for QSO / Listening on UDP 2333');
		httpServer = http.createServer(function (req, res) {
			res.setHeader('Access-Control-Allow-Origin', '*');
			res.writeHead(200, {'Content-Type': 'text/plain'});
			res.end('');
			const parts = req.url.substr(1).split('/');
			const qrg = parts[0];
			const mode = parts[1] || '';
			if (Number.isInteger(Number.parseInt(qrg))) {
				settrx(qrg,mode);
			}
		}).listen(54321);
	} catch(e) {
		tomsg('Some other Tool blocks Port 2333 or 54321. Stop it, and restart this');
	}
}


async function get_modes() {
	return new Promise((resolve) => {
		ipcMain.once('get_info_result', (event, modes) => {
			resolve(modes);
		});
		s_mainWindow.webContents.send('get_info', 'rig.get_modes');
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
		let postData= '<?xml version="1.0"?>';
		postData+='<methodCall><methodName>main.set_frequency</methodName><params><param><value><double>' + to.qrg + '</double></value></param></params></methodCall>';
		let options = {
			method: 'POST',
			headers: {
				'User-Agent': 'SW2WL_v' + app.getVersion(),
				'Content-Length': postData.length
			}
		};
		let url="http://"+defaultcfg.profiles[defaultcfg.profile ?? 0].flrig_host+':'+defaultcfg.profiles[defaultcfg.profile ?? 0].flrig_port+'/';
		let x=await httpPost(url,options,postData);

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
	}
	if (defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_ena) {
		const client = net.createConnection({ host: defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_host, port: defaultcfg.profiles[defaultcfg.profile ?? 0].hamlib_port }, () => {
			client.write("F " + to.qrg + "\n");
			if (defaultcfg.profiles[defaultcfg.profile ?? 0].wavelog_pmode) {
				client.write("M " + to.mode + "\n-1");
			}
			client.end();
		});

		client.on("error", (err) => {});
		client.on("close", () => {});
	}

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
