import type { Capability } from "./types.ts";

export class CapabilityRegistry {
  private capabilities = new Map<string, Capability>();

  register(capability: Capability): void {
    this.capabilities.set(capability.name, capability);
  }

  get(name: string): Capability | undefined {
    return this.capabilities.get(name);
  }

  list(): Capability[] {
    return [...this.capabilities.values()];
  }

  has(name: string): boolean {
    return this.capabilities.has(name);
  }

  async execute(
    name: string,
    input: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
    const cap = this.capabilities.get(name);
    if (!cap) throw new Error(`Capability not found: ${name}`);
    return cap.execute(input);
  }
}
