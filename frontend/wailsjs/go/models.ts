export namespace cert {
	
	export class Info {
	    caCertPath: string;
	    certPath: string;
	    exists: boolean;
	    isInstalled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.caCertPath = source["caCertPath"];
	        this.certPath = source["certPath"];
	        this.exists = source["exists"];
	        this.isInstalled = source["isInstalled"];
	    }
	}
	export class InstallResult {
	    success: boolean;
	    message: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new InstallResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.command = source["command"];
	    }
	}

}

export namespace config {
	
	export class Profile {
	    wavelog_url: string;
	    wavelog_key: string;
	    wavelog_id: string;
	    wavelog_radioname: string;
	    wavelog_pmode: boolean;
	    flrig_host: string;
	    flrig_port: string;
	    flrig_ena: boolean;
	    hamlib_host: string;
	    hamlib_port: string;
	    hamlib_ena: boolean;
	    ignore_pwr: boolean;
	    rotator_enabled: boolean;
	    rotator_host: string;
	    rotator_port: string;
	    rotator_threshold_az: number;
	    rotator_threshold_el: number;
	    rotator_park_az: number;
	    rotator_park_el: number;
	    hamlib_managed: boolean;
	    hamlib_model: number;
	    hamlib_device: string;
	    hamlib_baud: number;
	    hamlib_parity: string;
	    hamlib_stop_bits: number;
	    hamlib_handshake: string;
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.wavelog_url = source["wavelog_url"];
	        this.wavelog_key = source["wavelog_key"];
	        this.wavelog_id = source["wavelog_id"];
	        this.wavelog_radioname = source["wavelog_radioname"];
	        this.wavelog_pmode = source["wavelog_pmode"];
	        this.flrig_host = source["flrig_host"];
	        this.flrig_port = source["flrig_port"];
	        this.flrig_ena = source["flrig_ena"];
	        this.hamlib_host = source["hamlib_host"];
	        this.hamlib_port = source["hamlib_port"];
	        this.hamlib_ena = source["hamlib_ena"];
	        this.ignore_pwr = source["ignore_pwr"];
	        this.rotator_enabled = source["rotator_enabled"];
	        this.rotator_host = source["rotator_host"];
	        this.rotator_port = source["rotator_port"];
	        this.rotator_threshold_az = source["rotator_threshold_az"];
	        this.rotator_threshold_el = source["rotator_threshold_el"];
	        this.rotator_park_az = source["rotator_park_az"];
	        this.rotator_park_el = source["rotator_park_el"];
	        this.hamlib_managed = source["hamlib_managed"];
	        this.hamlib_model = source["hamlib_model"];
	        this.hamlib_device = source["hamlib_device"];
	        this.hamlib_baud = source["hamlib_baud"];
	        this.hamlib_parity = source["hamlib_parity"];
	        this.hamlib_stop_bits = source["hamlib_stop_bits"];
	        this.hamlib_handshake = source["hamlib_handshake"];
	    }
	}
	export class Config {
	    version: number;
	    profile: number;
	    profileNames: string[];
	    udp_enabled: boolean;
	    udp_port: number;
	    minimap_enabled: boolean;
	    profiles: Profile[];
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.profile = source["profile"];
	        this.profileNames = source["profileNames"];
	        this.udp_enabled = source["udp_enabled"];
	        this.udp_port = source["udp_port"];
	        this.minimap_enabled = source["minimap_enabled"];
	        this.profiles = this.convertValues(source["profiles"], Profile);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace hamlib {
	
	export class RadioModel {
	    id: number;
	    manufacturer: string;
	    model: string;
	
	    static createFrom(source: any = {}) {
	        return new RadioModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.manufacturer = source["manufacturer"];
	        this.model = source["model"];
	    }
	}

}

export namespace main {
	
	export class DownloadResult {
	    success: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new DownloadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	    }
	}
	export class HamlibStatus {
	    installed: boolean;
	    version: string;
	    running: boolean;
	    statusMsg: string;
	    installGuide: string;
	    canDownload: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HamlibStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = source["installed"];
	        this.version = source["version"];
	        this.running = source["running"];
	        this.statusMsg = source["statusMsg"];
	        this.installGuide = source["installGuide"];
	        this.canDownload = source["canDownload"];
	    }
	}
	export class RotatorStatus {
	    connected: boolean;
	    az: number;
	    el: number;
	    followMode: string;
	
	    static createFrom(source: any = {}) {
	        return new RotatorStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.az = source["az"];
	        this.el = source["el"];
	        this.followMode = source["followMode"];
	    }
	}
	export class TestResult {
	    success: boolean;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.reason = source["reason"];
	    }
	}
	export class UDPStatus {
	    enabled: boolean;
	    port: number;
	    running: boolean;
	    minimapEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UDPStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.port = source["port"];
	        this.running = source["running"];
	        this.minimapEnabled = source["minimapEnabled"];
	    }
	}

}

export namespace wavelog {
	
	export class Station {
	    station_profile_name: string;
	    station_callsign: string;
	    station_id: string;
	
	    static createFrom(source: any = {}) {
	        return new Station(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.station_profile_name = source["station_profile_name"];
	        this.station_callsign = source["station_callsign"];
	        this.station_id = source["station_id"];
	    }
	}

}

