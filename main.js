const {app, BrowserWindow, globalShortcut } = require('electron/main');
const path = require('node:path');
const {ipcMain} = require('electron')
const http = require('http');
const xml = require("xml2js");
const fs = require('fs');

let s_mainWindow;
let msgbacklog=[];
var WServer;

const DemoAdif='<call:5>DJ7NT <gridsquare:4>JO30 <mode:3>FT8 <rst_sent:3>-15 <rst_rcvd:2>33 <qso_date:8>20240110 <time_on:6>051855 <qso_date_off:8>20240110 <time_off:6>051855 <band:3>40m <freq:8>7.155783 <station_callsign:5>TE1ST <my_gridsquare:6>JO30OO <eor>';

if (require('electron-squirrel-startup')) app.quit();

var udp = require('dgram');

var q={};
var defaultcfg = {
	wavelog_failover_file: '/tmp/not_pushed_by_WLG.adif',
	wavelog_url: "https://log.jo30.de/index.php",
	wavelog_key: "mykey",
	wavelog_id: 0,
	wavelog_radioname: 'WLGate',
	wavelog_pmode: true,
	flrig_host: '127.0.0.1',
	flrig_port: '12345',
	flrig_ena: false,
}

const storage = require('electron-json-storage');

app.disableHardwareAcceleration(); 

storage.has('basic', function(error, hasKey) {
	if (!(hasKey)) {
		storage.set('basic', defaultcfg, function(e) {
			if (e) throw e;
		});
	} else {
		Object.assign(defaultcfg,storage.getSync('basic'));
	}
});

function createWindow () {
	const mainWindow = new BrowserWindow({
		width: 430,
		height: 250,
		resizable: false,
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
	 	mainWindow.setMenu(null);
	}


	mainWindow.loadFile('index.html')
	mainWindow.setTitle(require('./package.json').name + " V" + require('./package.json').version);

	return mainWindow;
}

ipcMain.on("set_config", async (event,arg) => {
	// event.returnValue="aha";
 	defaultcfg=arg;
	storage.set('basic', defaultcfg, function(e) {
		if (e) throw e;
	});
	event.returnValue=defaultcfg;
});

ipcMain.on("resize", async (event,arg) => {
	// event.returnValue="aha";
	newsize=arg;
	s_mainWindow.setContentSize(newsize.width,newsize.height,newsize.ani);
	s_mainWindow.setSize(newsize.width,newsize.height,newsize.ani);
	event.returnValue=true;
});

ipcMain.on("get_config", async (event,arg) => {
	Object.assign(defaultcfg,storage.getSync('basic'));
	defaultcfg=storage.getSync('basic')
	event.returnValue=defaultcfg;
});

ipcMain.on("setCAT", async (event,arg) => {
	settrx(arg);
	event.returnValue=true;
});

ipcMain.on("quit", async (event,arg) => {
	app.quit();
	event.returnValue=true;
});

ipcMain.on("test", async (event,arg) => {
	let result={};
	let plain;
	try {
		plain=await send2wavelog(arg,DemoAdif, true);
	} catch (e) {
		plain=e;
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

app.whenReady().then(() => {
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
})

app.on('window-all-closed', function () {
	if (process.platform !== 'darwin') app.quit()
	app.quit();
})

function parseADIF(adifdata) {
	const { ADIF } = require("tcadif");
	var adiReader = ADIF.parse(adifdata);
	return adiReader.toObject();
}

function writeADIF(adifObject) {
	const { ADIF } = require("tcadif");
	var adiWriter = new ADIF(adifObject);
	// console.log(adiWriter);
	return adiWriter;
}

function send2wavelog(o_cfg,adif, dryrun = false) {
	let clpayload={};
	clpayload.key=o_cfg.wavelog_key.trim();
	clpayload.station_profile_id=o_cfg.wavelog_id.trim();
	clpayload.type='adif';
	clpayload.string=adif;
	// console.log(clpayload);
	postData=JSON.stringify(clpayload);
	const https = require('https');
	var options = {
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
		rej=false;
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
				var resString = Buffer.concat(body).toString();
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
			writeToFile(adif, o_cfg.wavelog_failover_file);
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"internet problem"}';
			reject(result);
		})

		req.on('timeout', (err) => {
			writeToFile(adif, o_cfg.wavelog_failover_file);
			rej=true;
			req.destroy();
			result.resString='{"status":"failed","reason":"timeout"}';
			reject(result);
		})

		req.write(postData);
		req.end();
	});

}

WServer = udp.createSocket('udp4');
WServer.on('error', function(err) {
	tomsg('Some other Tool blocks Port 2333. Stop it, and restart this');
});

WServer.on('message',async function(msg,info){
	parsedXML={};
	adobject={};
	if (msg.toString().includes("xml")) {	// detect if incoming String is XML
		try {
			xml.parseString(msg.toString(), function (err,dat) {
				parsedXML=dat;
			});
			let qsodatum = new Date(Date.parse(parsedXML.contactinfo.timestamp[0]+"Z")); // Added Z to make it UTC
			qsodat=fmt(qsodatum);
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
		} catch (e) {}
	} else {
		try {
			adobject=parseADIF(msg.toString());
		} catch(e) {
			tomsg('<div class="alert alert-danger" role="alert">Received broken ADIF</div>');
			return;
		}
	}
	var plainret='';
	if (adobject.qsos.length>0) {
		let x={};
		try {
			outadif=writeADIF(adobject);
			plainret=await send2wavelog(defaultcfg,outadif.stringify());
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
		} else {
			adobject.created=false;
			adobject.fail=x;
		}
		s_mainWindow.webContents.send('updateTX', adobject);
		tomsg('');
	} else {
		tomsg('<div class="alert alert-danger" role="alert">Set ONLY Secondary UDP-Server to Port 2333 at WSTJ-X</div>');
	}
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
		WServer.bind(2333);
		tomsg('Waiting for QSO / Listening on UDP 2333');
		http.createServer(function (req, res) {
			res.setHeader('Access-Control-Allow-Origin', '*');
			res.writeHead(200, {'Content-Type': 'text/plain'});
			res.end('');
			let qrg=req.url.substr(1);
			if (Number.isInteger(Number.parseInt(qrg))) {
				settrx(qrg);
			}
		}).listen(54321);
	} catch(e) {
		tomsg('Some other Tool blocks Port 2333 or 54321. Stop it, and restart this');
	}
}

async function settrx(qrg) {
	let to={};
	to.qrg=qrg;
	if ((to.qrg) < 7999000) {
		to.mode='LSB';
	} else {
		to.mode='USB';
	}
	postData= '<?xml version="1.0"?>';
	postData+='<methodCall><methodName>main.set_frequency</methodName><params><param><value><double>' + to.qrg + '</double></value></param></params></methodCall>';
	var options = {
		method: 'POST',
		headers: {
			'User-Agent': 'SW2WL_v' + app.getVersion(),
			'Content-Length': postData.length
		}
	};
	let url="http://"+defaultcfg.flrig_host+':'+defaultcfg.flrig_port+'/';
	x=await httpPost(url,options,postData);

	if (defaultcfg.wavelog_pmode) {
		postData= '<?xml version="1.0"?>';
		postData+='<methodCall><methodName>rig.set_modeA</methodName><params><param><value>' + to.mode + '</value></param></params></methodCall>';
		var options = {
			method: 'POST',
			headers: {
				'User-Agent': 'SW2WL_v' + app.getVersion(),
				'Content-Length': postData.length
			}
		};
		x=await httpPost(url,options,postData);
	}

	return true;
}

function httpPost(url,options,postData) {
	return new Promise((resolve, reject) => {
		rej=false;
		let result={};
		const req = http.request(url,options, (res) => {
			let body=[];
			res.on('data', (chunk) => body.push(chunk));
			res.on('end', () => {
				var resString = Buffer.concat(body).toString();
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
	retstr={};
	d=spotDate.getUTCDate().toString();
	y=spotDate.getUTCFullYear().toString();
	m=(1+spotDate.getUTCMonth()).toString();
	h=spotDate.getUTCHours().toString();
	i=spotDate.getUTCMinutes().toString();
	s=spotDate.getUTCSeconds().toString();
	retstr.d=y.padStart(4,'0')+m.padStart(2,'0')+d.padStart(2,'0');
	retstr.t=h.padStart(2,'0')+i.padStart(2,'0')+s.padStart(2,'0');
	return retstr;
}

function writeToFile(adif, path) {
	if (path != undefined && path != "") {
		fs.appendFile(path, adif + '\n', (err) => {
			if (err) {
				// err
			}
		});
	}
}

startserver();
