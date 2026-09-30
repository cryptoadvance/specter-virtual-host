export namespace model {

	export class PendingRequest {
	    id: string;
	    origin: string;
	    // Go type: time
	    requestedAt: any;
	    // Go type: time
	    expiresAt: any;

	    static createFrom(source: any = {}) {
	        return new PendingRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.origin = source["origin"];
	        this.requestedAt = this.convertValues(source["requestedAt"], null);
	        this.expiresAt = this.convertValues(source["expiresAt"], null);
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
