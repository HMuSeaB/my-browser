export namespace main {
	
	export class AutomationInfo {
	    enabled: boolean;
	    listen_addr: string;
	    base_url: string;
	    auth_scheme: string;
	    protocol: string;
	    session_count: number;
	    token_configured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AutomationInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.listen_addr = source["listen_addr"];
	        this.base_url = source["base_url"];
	        this.auth_scheme = source["auth_scheme"];
	        this.protocol = source["protocol"];
	        this.session_count = source["session_count"];
	        this.token_configured = source["token_configured"];
	    }
	}
	export class AutomationSession {
	    session_id: string;
	    profile_id: string;
	    profile_name: string;
	    pid: number;
	    started_at: number;
	    status: string;
	    debug_port: number;
	    connect_url: string;
	    protocol: string;
	    start_url: string;
	    last_error: string;
	
	    static createFrom(source: any = {}) {
	        return new AutomationSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.profile_id = source["profile_id"];
	        this.profile_name = source["profile_name"];
	        this.pid = source["pid"];
	        this.started_at = source["started_at"];
	        this.status = source["status"];
	        this.debug_port = source["debug_port"];
	        this.connect_url = source["connect_url"];
	        this.protocol = source["protocol"];
	        this.start_url = source["start_url"];
	        this.last_error = source["last_error"];
	    }
	}
	export class BrowserProfile {
	    id: string;
	    name: string;
	    category: string;
	    proxy: string;
	    start_url: string;
	    ua: string;
	    platform: string;
	    cookies: string;
	    create_at: number;
	    enabled_scripts: string[];
	
	    static createFrom(source: any = {}) {
	        return new BrowserProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.proxy = source["proxy"];
	        this.start_url = source["start_url"];
	        this.ua = source["ua"];
	        this.platform = source["platform"];
	        this.cookies = source["cookies"];
	        this.create_at = source["create_at"];
	        this.enabled_scripts = source["enabled_scripts"];
	    }
	}
	export class ProxyEntry {
	    id: string;
	    name: string;
	    proxy: string;
	    status: string;
	    latency: string;
	    updated_at: number;
	
	    static createFrom(source: any = {}) {
	        return new ProxyEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.proxy = source["proxy"];
	        this.status = source["status"];
	        this.latency = source["latency"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class ScriptAsset {
	    name: string;
	    url: string;
	    file: string;
	
	    static createFrom(source: any = {}) {
	        return new ScriptAsset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.file = source["file"];
	    }
	}
	export class UserScript {
	    id: string;
	    name: string;
	    description: string;
	    version: string;
	    matches: string[];
	    run_at: string;
	    world: string;
	    grants: string[];
	    requires: string[];
	    resources: string[];
	    require_assets: ScriptAsset[];
	    resource_assets: ScriptAsset[];
	    enabled: boolean;
	    updated_at: number;
	
	    static createFrom(source: any = {}) {
	        return new UserScript(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.matches = source["matches"];
	        this.run_at = source["run_at"];
	        this.world = source["world"];
	        this.grants = source["grants"];
	        this.requires = source["requires"];
	        this.resources = source["resources"];
	        this.require_assets = this.convertValues(source["require_assets"], ScriptAsset);
	        this.resource_assets = this.convertValues(source["resource_assets"], ScriptAsset);
	        this.enabled = source["enabled"];
	        this.updated_at = source["updated_at"];
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
	export class ScriptInstallOutcome {
	    file_name: string;
	    ok: boolean;
	    error: string;
	    script: UserScript;
	    unsupported: string[];
	
	    static createFrom(source: any = {}) {
	        return new ScriptInstallOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file_name = source["file_name"];
	        this.ok = source["ok"];
	        this.error = source["error"];
	        this.script = this.convertValues(source["script"], UserScript);
	        this.unsupported = source["unsupported"];
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

