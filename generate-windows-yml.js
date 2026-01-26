const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execSync } = require('child_process');

// Get repository info dynamically
function getRepoInfo() {
  if (process.env.GITHUB_REPOSITORY) {
    const [owner, name] = process.env.GITHUB_REPOSITORY.split('/');
    return { owner, name };
  }

  try {
    const remoteUrl = execSync('git remote get-url origin', { encoding: 'utf8' }).trim();
    const match = remoteUrl.match(/(?:github\.com[/:]|github\.com\/)([^/]+)\/([^/]+?)(?:\.git)?$/i);
    if (match) {
      return { owner: match[1], name: match[2] };
    }
  } catch (e) {
    console.log('Could not determine repository from git remote:', e.message);
  }

  return { owner: 'wavelog', name: 'WaveLogGate' };
}

function getFileHash(filePath) {
  const content = fs.readFileSync(filePath);
  return crypto.createHash('sha512').update(content).digest('hex');
}

function getFileSize(filePath) {
  const stats = fs.statSync(filePath);
  return stats.size;
}

function generateWindowsUpdateYml() {
  const packageJson = require('./package.json');
  const version = packageJson.version;
  const repoInfo = getRepoInfo();

  // Find the make output directory
  const outDir = path.join(__dirname, 'out', 'make');

  // Look for Squirrel output in squirrel.windows or x64 directories
  const possiblePaths = [
    path.join(outDir, 'squirrel.windows', 'x64'),
    path.join(outDir, 'squirrel.windows'),
    path.join(outDir, 'x64')
  ];

  let squirrelDir = null;
  for (const p of possiblePaths) {
    if (fs.existsSync(p)) {
      const files = fs.readdirSync(p);
      if (files.includes('RELEASES') && files.some(f => f.endsWith('.nupkg'))) {
        squirrelDir = p;
        break;
      }
    }
  }

  if (!squirrelDir) {
    console.error('Could not find Squirrel build output directory');
    process.exit(1);
  }

  console.log('Found Squirrel directory:', squirrelDir);

  // Find the full nupkg file
  const nupkgFiles = fs.readdirSync(squirrelDir).filter(f => f.endsWith('-full.nupkg'));
  if (nupkgFiles.length === 0) {
    console.error('Could not find -full.nupkg file');
    process.exit(1);
  }

  const nupkgFile = nupkgFiles[0];
  const nupkgPath = path.join(squirrelDir, nupkgFile);
  const nupkgSize = getFileSize(nupkgPath);
  const nupkgSha512 = getFileHash(nupkgPath);

  // Read RELEASES file to get the filename (if it exists)
  let releasesFilename = nupkgFile;  // Default to the nupkg filename we found

  const releasesPath = path.join(squirrelDir, 'RELEASES');
  if (fs.existsSync(releasesPath)) {
    const releasesContent = fs.readFileSync(releasesPath, 'utf8');
    console.log('RELEASES file content:', releasesContent);

    // Try various patterns for RELEASES file format
    // Pattern 1: * filename hash
    let match = releasesContent.match(/\*?\s*([^\s*]+?\.nupkg)/);
    // Pattern 2: Just find any .nupkg filename
    if (!match) {
      match = releasesContent.match(/([a-zA-Z0-9_\-\.]+\.nupkg)/);
    }
    if (match) {
      releasesFilename = match[1];
      console.log('Parsed filename from RELEASES:', releasesFilename);
    }
  } else {
    console.log('RELEASES file not found, using nupkg filename directly');
  }

  // Generate the YAML content
  // For GitHub releases, the URL will be constructed by electron-updater
  const yamlContent = `version: ${version}
files:
  - url: ${releasesFilename}
    sha512: ${nupkgSha512}
    size: ${nupkgSize}
path: ${releasesFilename}
sha512: ${nupkgSha512}
size: ${nupkgSize}
releaseDate: ${new Date().toISOString()}
`;

  // Write to output directory
  const outputPath = path.join(outDir, 'latest.yml');
  fs.writeFileSync(outputPath, yamlContent);

  console.log(`Generated latest.yml at ${outputPath}`);
  console.log(`Version: ${version}`);
  console.log(`Package: ${releasesFilename}`);
  console.log(`Size: ${nupkgSize} bytes`);
}

generateWindowsUpdateYml();
