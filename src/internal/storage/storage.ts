import { Database } from "bun:sqlite";
import { dirname } from "node:path";

export class Storage {
  private db: Database;

  constructor(dbPath: string) {
    const dir = dirname(dbPath);
    Bun.spawnSync(["mkdir", "-p", dir]);
    this.db = new Database(dbPath, { create: true });
    this.db.exec("PRAGMA journal_mode = WAL");
    this.db.exec("PRAGMA foreign_keys = ON");
    this.migrate();
  }

  private migrate(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS tasks (
        id TEXT PRIMARY KEY,
        description TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        intent TEXT NOT NULL,
        created_at TEXT NOT NULL,
        completed_at TEXT
      );

      CREATE TABLE IF NOT EXISTS runs (
        id TEXT PRIMARY KEY,
        task_id TEXT REFERENCES tasks(id),
        agent TEXT NOT NULL,
        action TEXT NOT NULL,
        detail TEXT,
        created_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS patches (
        id TEXT PRIMARY KEY,
        file_path TEXT NOT NULL,
        diff TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        created_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS file_changes (
        id TEXT PRIMARY KEY,
        path TEXT NOT NULL,
        type TEXT NOT NULL,
        task_id TEXT REFERENCES tasks(id),
        created_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS messages (
        id TEXT PRIMARY KEY,
        role TEXT NOT NULL,
        content TEXT NOT NULL,
        task_id TEXT REFERENCES tasks(id),
        created_at TEXT NOT NULL
      );
    `);
  }

  getDb(): Database {
    return this.db;
  }

  createTask(
    id: string,
    description: string,
    intent: string,
  ): void {
    this.db.run(
      "INSERT INTO tasks (id, description, status, intent, created_at) VALUES (?, ?, 'pending', ?, ?)",
      [id, description, intent, new Date().toISOString()],
    );
  }

  updateTaskStatus(id: string, status: string): void {
    const completedAt =
      status === "completed" || status === "failed"
        ? new Date().toISOString()
        : null;
    this.db.run(
      "UPDATE tasks SET status = ?, completed_at = ? WHERE id = ?",
      [status, completedAt, id],
    );
  }

  addPatch(id: string, filePath: string, diff: string): void {
    this.db.run(
      "INSERT INTO patches (id, file_path, diff, status, created_at) VALUES (?, ?, ?, 'pending', ?)",
      [id, filePath, diff, new Date().toISOString()],
    );
  }

  updatePatchStatus(id: string, status: string): void {
    this.db.run("UPDATE patches SET status = ? WHERE id = ?", [status, id]);
  }

  getPatches(status?: string): Array<{
    id: string;
    filePath: string;
    diff: string;
    status: string;
  }> {
    const sql =
      status
        ? "SELECT id, file_path as filePath, diff, status FROM patches WHERE status = ?"
        : "SELECT id, file_path as filePath, diff, status FROM patches";
    const params = status ? [status] : [];
    return this.db.query(sql).all(...params) as Array<{
      id: string;
      filePath: string;
      diff: string;
      status: string;
    }>;
  }

  addFileChange(
    id: string,
    path: string,
    type: string,
    taskId: string,
  ): void {
    this.db.run(
      "INSERT INTO file_changes (id, path, type, task_id, created_at) VALUES (?, ?, ?, ?, ?)",
      [id, path, type, taskId, new Date().toISOString()],
    );
  }

  getFileChanges(taskId?: string): Array<{
    id: string;
    path: string;
    type: string;
  }> {
    const sql = taskId
      ? "SELECT id, path, type FROM file_changes WHERE task_id = ?"
      : "SELECT id, path, type FROM file_changes";
    const params = taskId ? [taskId] : [];
    return this.db.query(sql).all(...params) as Array<{
      id: string;
      path: string;
      type: string;
    }>;
  }

  addMessage(
    id: string,
    role: string,
    content: string,
    taskId: string,
  ): void {
    this.db.run(
      "INSERT INTO messages (id, role, content, task_id, created_at) VALUES (?, ?, ?, ?, ?)",
      [id, role, content, taskId, new Date().toISOString()],
    );
  }

  getMessages(taskId: string): Array<{
    id: string;
    role: string;
    content: string;
  }> {
    return this.db
      .query("SELECT id, role, content FROM messages WHERE task_id = ? ORDER BY created_at")
      .all(taskId) as Array<{ id: string; role: string; content: string }>;
  }

  close(): void {
    this.db.close();
  }
}
