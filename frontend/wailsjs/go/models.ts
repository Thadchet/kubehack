export namespace main {
	
	export class Connection {
	    baseUrl: string;
	    token: string;
	
	    static createFrom(source: any = {}) {
	        return new Connection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseUrl = source["baseUrl"];
	        this.token = source["token"];
	    }
	}

}

