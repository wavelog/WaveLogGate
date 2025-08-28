// This file is required by the index.html file and will
// be executed in the renderer process for that window.
// No Node.js APIs are available in this process because
// `nodeIntegration` is turned off. Use `preload.js` to
// selectively enable features needed in the rendering
// process.


// Shorthand for document.querySelector.
var cfg={};
var active_cfg=0;
var trxpoll=undefined;

const {ipcRenderer} = require('electron')
const net = require('net');

const bt_toggle=select("#toggle");
const bt_save=select("#save");
const bt_quit=select("#quit");
const bt_test=select("#test");
const input_key=select("#wavelog_key");
const input_url=select("#wavelog_url");

var oldCat={ vfo: 0, mode: "SSB" };

$(document).ready(function() {

	load_config();

	bt_toggle.addEventListener('click', async () => {
		if ($("#toggle").html() == '1') {
			$("#toggle").html("2");
			active_cfg=1;
			await load_config();
		} else {
			$("#toggle").html("1");
			active_cfg=0;
			await load_config();
		}
		$("#test").removeClass('btn-success');
		$("#test").removeClass('btn-danger');
		$("#test").addClass('btn-primary');
	});

	bt_save.addEventListener('click', async () => {
		// cfg=ipcRenderer.sendSync("get_config", active_cfg);
		cfg.profile=active_cfg;
		cfg.profiles[cfg.profile].wavelog_url=$("#wavelog_url").val().trim();
		cfg.profiles[cfg.profile].wavelog_key=$("#wavelog_key").val().trim();
		cfg.profiles[cfg.profile].wavelog_id=$("#wavelog_id").val().trim();
		cfg.profiles[cfg.profile].wavelog_radioname=$("#wavelog_radioname").val().trim();
		cfg.profiles[cfg.profile].flrig_host=$("#flrig_host").val().trim();
		cfg.profiles[cfg.profile].flrig_port=$("#flrig_port").val().trim();
		cfg.profiles[cfg.profile].flrig_ena=$("#flrig_ena").is(':checked');
		cfg.profiles[cfg.profile].hamlib_ena=$("#hamlib_ena").is(':checked');
		cfg.profiles[cfg.profile].wavelog_pmode=$("#wavelog_pmode").is(':checked');

		// advanced
		if ($("#flrig_ena").is(':checked') && cfg.profiles[cfg.profile].hamlib_ena){
			cfg.profiles[cfg.profile].hamlib_ena = false;
		}

		cfg=await ipcRenderer.sendSync("set_config", cfg);
	});

	bt_quit.addEventListener('click', () => {
		x=ipcRenderer.sendSync("quit", '');
	});

	bt_test.addEventListener('click', () => {
		cfg.profiles[active_cfg].wavelog_url=$("#wavelog_url").val().trim();
		cfg.profiles[active_cfg].wavelog_key=$("#wavelog_key").val().trim();
		cfg.profiles[active_cfg].wavelog_id=$("#wavelog_id").val().trim();
		cfg.profiles[active_cfg].wavelog_radioname=$("#wavelog_radioname").val().trim();
		x=(ipcRenderer.sendSync("test", cfg.profiles[active_cfg]));
		if (x.payload.status == 'created') {
			$("#test").removeClass('btn-primary');
			$("#test").removeClass('btn-danger');
			$("#test").addClass('btn-success');
			$("#msg2").hide();
			$("#msg2").html("");
		} else {
			$("#test").removeClass('btn-primary');
			$("#test").removeClass('btn-success');
			$("#test").addClass('btn-danger');
			$("#msg2").show();
			$("#msg2").html("Test failed. Reason: "+x.payload.reason);
		}
	});

	input_key.addEventListener('change', () => {
		getStations();
	});
	input_url.addEventListener('change', () => {
		getStations();
	});
	$('#reload_icon').on('click', () => {
		getStations();
	});

	setInterval(updateUtcTime, 1000);
	window.onload = updateUtcTime;

	$("#config-tab").on("click",function() {
		obj={};
		obj.width=430;
		obj.height=550;
		obj.ani=false;
		resizeme(obj);
	});

	$("#status-tab").on("click",function() {
		obj={};
		obj.width=430;
		obj.height=250;
		obj.ani=false;
		resizeme(obj);
	});

	ipcRenderer.on('get_info', async (event, arg) => {
		const result = await getInfo(arg); 
		ipcRenderer.send('get_info_result', result);
	});
});

async function load_config() {
	cfg=await ipcRenderer.sendSync("get_config", active_cfg);
	$("#toggle").html((cfg.profile || 0)+1);
	$("#wavelog_url").val(cfg.profiles[active_cfg].wavelog_url);
	$("#wavelog_key").val(cfg.profiles[active_cfg].wavelog_key);
	// $("#wavelog_id").val(cfg.wavelog_id);
	$("#wavelog_radioname").val(cfg.profiles[active_cfg].wavelog_radioname);
	$("#flrig_host").val(cfg.profiles[active_cfg].flrig_host);
	$("#flrig_port").val(cfg.profiles[active_cfg].flrig_port);
	$("#flrig_ena").prop("checked", cfg.profiles[active_cfg].flrig_ena);
	$("#wavelog_pmode").prop("checked", cfg.profiles[active_cfg].wavelog_pmode);

	if (cfg.profiles[active_cfg].wavelog_key != "" && cfg.profiles[active_cfg].wavelog_url != "") {
		getStations();
	}
	if (trxpoll === undefined) {
		getsettrx();
	}
}

function resizeme(size) {
	x=(ipcRenderer.sendSync("resize", size))
	return x;
}

function select(selector) {
	return document.querySelector(selector);
}

window.TX_API.onUpdateMsg((value) => {
	$("#msg").html(value);
	$("#msg2").html("");
});

window.TX_API.onUpdateTX((value) => {
	if (value.created) {
		$("#log").html('<div class="alert alert-success" role="alert">'+value.qsos[0].TIME_ON+" "+value.qsos[0].CALL+" ("+(value.qsos[0].GRIDSQUARE || 'No Grid')+") on "+(value.qsos[0].BAND || 'No BAND')+" (R:"+(value.qsos[0].RST_RCVD || 'No RST')+" / S:"+(value.qsos[0].RST_SENT || 'No RST')+') - OK</div>');
	} else {
		$("#log").html('<div class="alert alert-danger" role="alert">'+value.qsos[0].TIME_ON+" "+value.qsos[0].CALL+" ("+(value.qsos[0].GRIDSQUARE || 'No Grid')+") on "+(value.qsos[0].BAND || 'NO BAND')+" (R:"+(value.qsos[0].RST_RCVD || 'No RST')+" / S:"+(value.qsos[0].RST_SENT || 'No RST')+') - Error<br/>Reason: '+value.fail.payload.reason+'</div>');
	}
})


async function get_trx() {
	let currentCat={};
	currentCat.vfo=await getInfo('rig.get_vfo');
	currentCat.mode=await getInfo('rig.get_mode');
	currentCat.ptt=await getInfo('rig.get_ptt');
	if(!cfg.profiles[active_cfg].ignore_pwr){
		currentCat.power=await getInfo('rig.get_power') ?? 0;
	}
	currentCat.split=await getInfo('rig.get_split');
	currentCat.vfoB=await getInfo('rig.get_vfoB');
	currentCat.modeB=await getInfo('rig.get_modeB');

	$("#current_trx").html((currentCat.vfo/(1000*1000))+" MHz / "+currentCat.mode);
	if (!(isDeepEqual(oldCat,currentCat))) {
		console.log(await informWavelog(currentCat));
	}
	oldCat=currentCat;
	return currentCat;
}

async function getInfo(which) {
	if (cfg.profiles[active_cfg].flrig_ena){
		try {
			const response = await fetch(
				"http://"+$("#flrig_host").val()+':'+$("#flrig_port").val(), {
					method: 'POST',
					// mode: 'no-cors',
					headers: {
						'Accept': 'application/json, application/xml, text/plain, text/html, *.*',
						'Content-Type': 'application/x-www-form-urlencoded; charset=utf-8'
					},
					body: '<?xml version="1.0"?><methodCall><methodName>'+which+'</methodName></methodCall>',
				}
			);
			const data = await response.text();
			const parser = new DOMParser();
			const xmlDoc = parser.parseFromString(data, "application/xml");

			const valueNode = xmlDoc.querySelector("methodResponse > params > param > value");

			if (!valueNode) {
				return null;
			}

			const arrayNode = valueNode.querySelector("array > data");
			if (arrayNode) {
				const items = Array.from(arrayNode.querySelectorAll("value string, value"))
				.map(node => node.textContent.trim());
				return items;
			} else {
				return valueNode.textContent.trim();
			}
		} catch (e) {
			return '';
		}
	}
	if (cfg.profiles[active_cfg].hamlib_ena) {
		var commands = {"rig.get_vfo": "f", "rig.get_mode": "m", "rig.get_ptt": 0, "rig.get_power": 0, "rig.get_split": 0, "rig.get_vfoB": 0, "rig.get_modeB": 0};

		const host = cfg.profiles[active_cfg].hamlib_host;
		const port = parseInt(cfg.profiles[active_cfg].hamlib_port, 10);

		return new Promise((resolve, reject) => {
			if (commands[which]) {
				const client = net.createConnection({ host, port }, () => client.write(commands[which]));
				client.on('data', (data) => {
					data = data.toString()
					if(data.startsWith("RPRT")){
						reject();
					} else {
						resolve(data.split('\n')[0]);
					}
					client.end();
				});
				client.on('error', (err) => reject());
				client.on("close", () => {});
			} else {
				resolve(undefined);
			}
		});
	}
}

async function getsettrx() {
	if ($("#flrig_ena").is(':checked') || cfg.profiles[active_cfg].hamlib_ena) {
		console.log('Polling TRX '+trxpoll);
		x=get_trx();
	}
	trxpoll = setTimeout(() => {
		getsettrx();
	}, 1000);
}

const isDeepEqual = (object1, object2) => {

	const objKeys1 = Object.keys(object1);
	const objKeys2 = Object.keys(object2);

	if (objKeys1.length !== objKeys2.length) return false;

	for (var key of objKeys1) {
		const value1 = object1[key];
		const value2 = object2[key];

		const isObjects = isObject(value1) && isObject(value2);

		if ((isObjects && !isDeepEqual(value1, value2)) ||
			(!isObjects && value1 !== value2)
		) {
			return false;
		}
	}
	return true;
};

const isObject = (object) => {
	return object != null && typeof object === "object";
};

async function informWavelog(CAT) {
	let data = {
		radio: "WLGate",
		key: cfg.profiles[active_cfg].wavelog_key,
		radio: cfg.profiles[active_cfg].wavelog_radioname
	};
	if (CAT.power !== undefined && CAT.power !== 0) {
		data.power = CAT.power;
	}
	// if (CAT.ptt !== undefined) {       // not impleented yet in Wavelog, so maybe later
	// 	data.ptt = CAT.ptt;
	// }
	if (CAT.split == '1') {
		// data.split=true;  // not implemented yet in Wavelog
		data.frequency=CAT.vfoB;
		data.mode=CAT.modeB;
		data.frequency_rx=CAT.vfo;
		data.mode_rx=CAT.mode;
	} else {
		data.frequency=CAT.vfo;
		data.mode=CAT.mode;
	}

	let x=await fetch(cfg.profiles[active_cfg].wavelog_url + '/api/radio', {
		method: 'POST',
		rejectUnauthorized: false,
		headers: {
			Accept: 'application.json',
			'Content-Type': 'application/json',
		},
		body: JSON.stringify(data)
	});
	return x;
}

function updateUtcTime() {
	const now = new Date();

	const hours = ('0' + now.getUTCHours()).slice(-2);
	const minutes = ('0' + now.getUTCMinutes()).slice(-2);
	const seconds = ('0' + now.getUTCSeconds()).slice(-2);

	const formattedTime = `${hours}:${minutes}:${seconds}z`;

	document.getElementById('utc').innerHTML = formattedTime;
}

async function getStations() {
	let select = $('#wavelog_id');
	select.empty();
	select.prop('disabled', true);
	try {
		let x = await fetch($('#wavelog_url').val().trim() + '/api/station_info/' + $('#wavelog_key').val().trim(), {
			method: 'GET',
			rejectUnauthorized: false,
			headers: {
				Accept: 'application/json',
				'Content-Type': 'application/json',
			},
		});

		if (!x.ok) {
			throw new Error(`HTTP error! Status: ${x.status}`);
		}

		let data = await x.json();
		fillDropdown(data);

	} catch (error) {
		select.append(new Option('Failed to load stations', '0'));
		console.error('Could not load station locations:', error.message);
	}
}

function fillDropdown(data) {
	let select = $('#wavelog_id');
	select.empty();
	select.prop('disabled', false);

	data.forEach(function(station) {
		let optionText = station.station_profile_name + " (" + station.station_callsign + ", ID: " + station.station_id + ")";
		let optionValue = station.station_id;
		select.append(new Option(optionText, optionValue));
	});

	if (cfg.profiles[active_cfg].wavelog_id && data.some(station => station.station_id == cfg.profiles[active_cfg].wavelog_id)) {
		select.val(cfg.profiles[active_cfg].wavelog_id);
	} else {
		select.val(data.length > 0 ? data[0].station_id : null);
	}
}
