export namespace main {
	
	export class BrowserProfile {
	    id: string;
	    name: string;
	    proxy: string;
	    start_url: string;
	    ua: string;
	    platform: string;
	    cookies: string;
	    create_at: number;
	
	    static createFrom(source: any = {}) {
	        return new BrowserProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.proxy = source["proxy"];
	        this.start_url = source["start_url"];
	        this.ua = source["ua"];
	        this.platform = source["platform"];
	        this.cookies = source["cookies"];
	        this.create_at = source["create_at"];
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

}

