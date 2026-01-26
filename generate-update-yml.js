const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const packageJson = require('./package.json');

const version = packageJson.version;
const appName = packageJson.productName || 'WaveLogGate';
const outDir = path.join(__dirname, 'out', 'make');

// Generate SHA512 hash of a file (electron-updater uses SHA512, not SHA256)
function getFileHash(filePath) {
  const content = fs.readFileSync(filePath);
  return crypto.createHash('sha512').update(content).digest('hex');
}

// Get file size in bytes
function getFileSize(filePath) {
  const stats = fs.statSync(filePath);
  return stats.size;
}

// Generate the YAML file for macOS updates
function generateMacUpdateYml() {
  const zipDir = path.join(outDir, 'zip');
  const files = [];

  // Check for both architectures
  const arches = [];
  if (fs.existsSync(path.join(zipDir, 'darwin', 'arm64'))) {
    arches.push('arm64');
  }
  if (fs.existsSync(path.join(zipDir, 'darwin', 'x64'))) {
    arches.push('x64');
  }

  if (arches.length === 0) {
    console.log('No macOS zip files found. Run "bun run make" first.');
    return false;
  }

  for (const arch of arches) {
    const archDir = path.join(zipDir, 'darwin', arch);
    const dirFiles = fs.readdirSync(archDir);
    const zipFile = dirFiles.find(f => f.endsWith('.zip') && f.includes(version));

    if (!zipFile) {
      console.log(`No zip file found for ${arch}`);
      continue;
    }

    const zipPath = path.join(archDir, zipFile);
    const hash = getFileHash(zipPath);
    const size = getFileSize(zipPath);
    const fileName = path.basename(zipFile);

    // The URL that will be used in the YAML file
    const url = `https://github.com/wavelog/WaveLogGate/releases/download/v${version}/${fileName}`;

    files.push({
      url,
      size,
      sha512: hash.toUpperCase()
    });

    console.log(`Found ${arch} build:`);
    console.log(`  File: ${fileName}`);
    console.log(`  Size: ${size} bytes`);
    console.log(`  SHA512: ${hash.substring(0, 32)}...`);
  }

  if (files.length === 0) {
    console.log('No valid zip files found');
    return false;
  }

  // Use the first file (prefer arm64 if available) as the primary entry
  // electron-updater will pick the right file based on the user's architecture
  const primaryFile = files.find(f => f.url.includes('arm64')) || files[0];

  const yamlContent = `version: ${version}
files:
${files.map(f => `  - url: ${f.url}
    size: ${f.size}
    sha512: ${f.sha512}`).join('\n')}
path: ${primaryFile.url.split('/').pop()}
sha512: ${primaryFile.sha512}
size: ${primaryFile.size}
releaseDate: ${new Date().toISOString()}
`;

  const outputPath = path.join(outDir, 'latest-mac.yml');
  fs.writeFileSync(outputPath, yamlContent);
  console.log(`\n✅ Created: ${outputPath}`);
  console.log(`\nThis file will be uploaded to GitHub release for auto-updates.`);
  return true;
}

generateMacUpdateYml();
