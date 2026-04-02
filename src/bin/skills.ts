import { join } from "path";
import { existsSync, mkdirSync } from "fs";

async function main() {
    const args = process.argv.slice(2);
    const command = args[0];
    const target = args[1];

    if (command !== "add" || !target) {
        console.log("Usage: npx skills add <github-repo-path>");
        process.exit(1);
    }

    const skillsDir = join(process.cwd(), ".rakitkode", "skills");
    if (!existsSync(skillsDir)) {
        mkdirSync(skillsDir, { recursive: true });
    }

    console.log(`🚀 Adding skills from ${target}...`);

    // Dynamic GitHub extractor for PawBytes Skill Suites
    if (target.includes("pawbytes/skill-suites")) {
        const repo = "pawbytes/skill-suites";
        const rootPath = "src/marketing";
        
        console.log(`🔍 Discovering suites in ${repo}/${rootPath}...`);
        
        try {
            // First, get the list of all suites in the rootPath
            const suitesRes = await fetch(`https://api.github.com/repos/${repo}/contents/${rootPath}`, {
                headers: { "User-Agent": "RakitKode-Skills-Installer" }
            });
            if (!suitesRes.ok) throw new Error(`Failed to list suites: ${suitesRes.statusText}`);
            
            const items = await suitesRes.json() as any[];
            const folders = items.filter(i => i.type === "dir").map(i => i.name);
            
            for (const folderName of folders) {
                const suiteLocalPath = join(skillsDir, folderName);
                console.log(`🚀 Installing suite: ${folderName}...`);
                
                await downloadGithubDir(repo, `${rootPath}/${folderName}`, suiteLocalPath);
                
                // Cleanup old flat file
                const oldFile = join(skillsDir, `${folderName}.md`);
                if (existsSync(oldFile)) {
                   const { unlinkSync } = require("fs");
                   unlinkSync(oldFile);
                }
                console.log(`  ✅ Installed ${folderName} completely`);
            }
        } catch (err: any) {
            console.error(`  ❌ Installation failed: ${err.message}`);
        }
    } else {
        console.error("Currently only pawbytes/skill-suites is supported for automated 'npx skills add'.");
    }
}

async function downloadGithubDir(repo: string, remotePath: string, localPath: string) {
    if (!existsSync(localPath)) mkdirSync(localPath, { recursive: true });
    
    const res = await fetch(`https://api.github.com/repos/${repo}/contents/${remotePath}`, {
        headers: { "User-Agent": "RakitKode-Skills-Installer" }
    });
    if (!res.ok) return;
    
    const items = await res.json() as any[];
    for (const item of items) {
        const currentLocal = join(localPath, item.name);
        if (item.type === "dir") {
            await downloadGithubDir(repo, item.path, currentLocal);
        } else if (item.type === "file") {
            const rawRes = await fetch(item.download_url);
            if (rawRes.ok) {
                await Bun.write(currentLocal, await rawRes.text());
                console.log(`    - Added: ${item.path.replace(remotePath + "/", "")}`);
            }
        }
    }
}

main();
