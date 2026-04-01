export interface Capability {
  name: string;
  description: string;
  parameters?: Record<string, unknown>;
  execute(input: Record<string, unknown>): Promise<Record<string, unknown>>;
}
