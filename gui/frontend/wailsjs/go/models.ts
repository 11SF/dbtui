export namespace config {
	
	export class TunnelConfig {
	    KubeContext: string;
	    Namespace: string;
	    TargetType: string;
	    TargetName: string;
	    RemotePort: number;
	    LocalPort: number;
	
	    static createFrom(source: any = {}) {
	        return new TunnelConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.KubeContext = source["KubeContext"];
	        this.Namespace = source["Namespace"];
	        this.TargetType = source["TargetType"];
	        this.TargetName = source["TargetName"];
	        this.RemotePort = source["RemotePort"];
	        this.LocalPort = source["LocalPort"];
	    }
	}
	export class Connection {
	    Name: string;
	    Type: string;
	    Host: string;
	    Port: number;
	    User: string;
	    DBName: string;
	    AuthSource: string;
	    Tunnel?: TunnelConfig;
	    Group: string;
	    QueryTimeoutSec: number;
	    ShowAllDatabases: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Connection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Type = source["Type"];
	        this.Host = source["Host"];
	        this.Port = source["Port"];
	        this.User = source["User"];
	        this.DBName = source["DBName"];
	        this.AuthSource = source["AuthSource"];
	        this.Tunnel = this.convertValues(source["Tunnel"], TunnelConfig);
	        this.Group = source["Group"];
	        this.QueryTimeoutSec = source["QueryTimeoutSec"];
	        this.ShowAllDatabases = source["ShowAllDatabases"];
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

export namespace db {
	
	export class ColumnInfo {
	    Name: string;
	    DataType: string;
	    Nullable: boolean;
	    Default: string;
	
	    static createFrom(source: any = {}) {
	        return new ColumnInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.DataType = source["DataType"];
	        this.Nullable = source["Nullable"];
	        this.Default = source["Default"];
	    }
	}
	export class DocResult {
	    Documents: string[];
	    Duration: number;
	
	    static createFrom(source: any = {}) {
	        return new DocResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Documents = source["Documents"];
	        this.Duration = source["Duration"];
	    }
	}
	export class ForeignKeyInfo {
	    Column: string;
	    RefSchema: string;
	    RefTable: string;
	    RefColumn: string;
	    ConstraintName: string;
	
	    static createFrom(source: any = {}) {
	        return new ForeignKeyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Column = source["Column"];
	        this.RefSchema = source["RefSchema"];
	        this.RefTable = source["RefTable"];
	        this.RefColumn = source["RefColumn"];
	        this.ConstraintName = source["ConstraintName"];
	    }
	}
	export class IndexInfo {
	    Name: string;
	    Columns: string[];
	    Unique: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IndexInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Columns = source["Columns"];
	        this.Unique = source["Unique"];
	    }
	}
	export class ZMember {
	    Member: string;
	    Score: number;
	
	    static createFrom(source: any = {}) {
	        return new ZMember(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Member = source["Member"];
	        this.Score = source["Score"];
	    }
	}
	export class KVValue {
	    Type: string;
	    String: string;
	    Hash: Record<string, string>;
	    List: string[];
	    Set: string[];
	    ZSet: ZMember[];
	
	    static createFrom(source: any = {}) {
	        return new KVValue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Type = source["Type"];
	        this.String = source["String"];
	        this.Hash = source["Hash"];
	        this.List = source["List"];
	        this.Set = source["Set"];
	        this.ZSet = this.convertValues(source["ZSet"], ZMember);
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
	export class QueryResult {
	    Columns: string[];
	    Rows: any[][];
	    Duration: number;
	    Capped: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QueryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Columns = source["Columns"];
	        this.Rows = source["Rows"];
	        this.Duration = source["Duration"];
	        this.Capped = source["Capped"];
	    }
	}
	export class TableDescription {
	    Columns: ColumnInfo[];
	    PrimaryKeys: string[];
	    ForeignKeys: ForeignKeyInfo[];
	    Indexes: IndexInfo[];
	
	    static createFrom(source: any = {}) {
	        return new TableDescription(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Columns = this.convertValues(source["Columns"], ColumnInfo);
	        this.PrimaryKeys = source["PrimaryKeys"];
	        this.ForeignKeys = this.convertValues(source["ForeignKeys"], ForeignKeyInfo);
	        this.Indexes = this.convertValues(source["Indexes"], IndexInfo);
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
	export class TableRef {
	    Schema: string;
	    Name: string;
	    Kind: string;
	
	    static createFrom(source: any = {}) {
	        return new TableRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Schema = source["Schema"];
	        this.Name = source["Name"];
	        this.Kind = source["Kind"];
	    }
	}

}

export namespace history {
	
	export class HistoryEntry {
	    ID: number;
	    // Go type: time
	    Timestamp: any;
	    ConnName: string;
	    Query: string;
	    DurationMs: number;
	    RowCount: number;
	    Error: string;
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Timestamp = this.convertValues(source["Timestamp"], null);
	        this.ConnName = source["ConnName"];
	        this.Query = source["Query"];
	        this.DurationMs = source["DurationMs"];
	        this.RowCount = source["RowCount"];
	        this.Error = source["Error"];
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

export namespace main {
	
	export class KVCommandResult {
	    result?: any;
	    needsConfirm: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KVCommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.result = source["result"];
	        this.needsConfirm = source["needsConfirm"];
	    }
	}
	export class StatusPayload {
	    name: string;
	    type: string;
	    category: string;
	    status: string;
	    dbName?: string;
	    showAllDatabases?: boolean;
	    tunnelLocalPort?: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.category = source["category"];
	        this.status = source["status"];
	        this.dbName = source["dbName"];
	        this.showAllDatabases = source["showAllDatabases"];
	        this.tunnelLocalPort = source["tunnelLocalPort"];
	        this.error = source["error"];
	    }
	}

}

