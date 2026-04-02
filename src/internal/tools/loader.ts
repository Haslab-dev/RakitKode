import type { Capability } from "../capability/types.ts";
import { CapabilityRegistry } from "../capability/registry.ts";
import { readdir, stat } from "fs/promises";
import { join } from "path";

export async function loadUserTools(registry: CapabilityRegistry, toolsDir: string): Promise<void> {
  try {
    const files = await readdir(toolsDir);
    for (const file of files) {
      if (file.endsWith(".ts") || file.endsWith(".js")) {
        try {
          // Dynamic import of user tools
          const module = await import(join(toolsDir, file));
          if (module.default && typeof module.default === "object" && "name" in module.default) {
            registry.register(module.default as Capability);
          } else if (typeof module.tool === "object" && module.tool.name) {
            registry.register(module.tool as Capability);
          }
        } catch (err) {
          console.error(`Failed to load user tool ${file}:`, err);
        }
      }
    }
  } catch (err) {
    // If directory doesn't exist, ignore
  }
}

export async function loadSkills(skillsDir: string, currentDir = ""): Promise<string[]> {
  const skills: string[] = [];
  const baseDir = currentDir ? join(skillsDir, currentDir) : skillsDir;
  
  try {
    const entries = await readdir(baseDir, { withFileTypes: true });
    for (const entry of entries) {
      const relPath = join(currentDir, entry.name);
      
      if (entry.isDirectory()) {
         const subSkills = await loadSkills(skillsDir, relPath);
         skills.push(...subSkills);
      } else if (entry.name.endsWith(".md")) {
        const isManifest = entry.name === "SKILL.md" || !currentDir;
        if (isManifest) {
            // Full Load for Manifests/Root files
            const content = await Bun.file(join(baseDir, entry.name)).text();
            skills.push(`Skill: ${relPath.replace(".md", "")}\nType: manifest\nInstructions:\n${content}`);
        } else {
            // Metadata Load for References to save context
            skills.push(`Reference-Knowledge: ${relPath.replace(".md", "")}\nType: reference-only (Read path ".rakitkode/skills/${relPath}" using read_file for details)`);
        }
      }
    }
  } catch (err) {}
  return skills;
}
