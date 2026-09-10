// Thin wrapper over the generated Wails bindings: normalizes Go error
// rejections (Wails serializes a Go `error` as a plain string, not an
// Error object) and re-exports the bound methods + model types under one
// import so views don't reach into ../../wailsjs directly.
import * as App from "../wailsjs/go/main/App";
import { EventsOn } from "../wailsjs/runtime/runtime";
import { config, db, history, main } from "../wailsjs/go/models";

export type Connection = config.Connection;
export type TunnelConfig = config.TunnelConfig;
export type QueryResult = db.QueryResult;
export type DocResult = db.DocResult;
export type TableRef = db.TableRef;
export type TableDescription = db.TableDescription;
export type KVValue = db.KVValue;
export type IndexInfo = db.IndexInfo;
export type HistoryEntry = history.HistoryEntry;
export type StatusPayload = main.StatusPayload;
export type KVCommandResult = main.KVCommandResult;

/** Turns any rejection (string, Error, or object) into a readable message. */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}

async function call<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (err) {
    throw new Error(errorMessage(err));
  }
}

export const api = {
  listConnections: () => call(App.ListConnections()),
  saveConnection: (originalName: string, conn: Connection, password: string) =>
    call(App.SaveConnection(originalName, conn, password)),
  deleteConnection: (name: string) => call(App.DeleteConnection(name)),

  connect: (name: string) => call(App.Connect(name)),
  disconnect: () => call(App.Disconnect()),
  getStatus: () => call(App.GetStatus()),

  listSchemas: () => call(App.ListSchemas()),
  listTables: (schema: string) => call(App.ListTables(schema)),
  describeTable: (schema: string, table: string) => call(App.DescribeTable(schema, table)),
  runQuery: (sql: string) => call(App.RunQuery(sql)),

  listRedisDatabases: () => call(App.ListRedisDatabases()),
  scanKeys: (dbIndex: number, pattern: string) => call(App.ScanKeys(dbIndex, pattern)),
  keyType: (dbIndex: number, key: string) => call(App.KeyType(dbIndex, key)),
  ttlSeconds: (dbIndex: number, key: string) => call(App.TTLSeconds(dbIndex, key)),
  getValue: (dbIndex: number, key: string) => call(App.GetValue(dbIndex, key)),
  runKVCommand: (dbIndex: number, commandLine: string, confirmed: boolean) =>
    call(App.RunKVCommand(dbIndex, commandLine, confirmed)),

  listMongoDatabases: () => call(App.ListMongoDatabases()),
  listCollections: (database: string) => call(App.ListCollections(database)),
  indexInfo: (database: string, collection: string) => call(App.IndexInfo(database, collection)),
  find: (database: string, collection: string, filterJSON: string) =>
    call(App.Find(database, collection, filterJSON)),

  getHistory: (limit: number, connNameFilter: string) => call(App.GetHistory(limit, connNameFilter)),

  exportResultCSV: () => call(App.ExportResultCSV()),
  exportDocResultJSONL: () => call(App.ExportDocResultJSONL()),

  getLogs: () => call(App.GetLogs()),
};

/** Subscribes to the Go side's "status" runtime event (emitted at every
 * connect/disconnect transition). Returns an unsubscribe function. */
export function onStatus(cb: (status: StatusPayload) => void): () => void {
  return EventsOn("status", (payload: StatusPayload) => cb(payload));
}
