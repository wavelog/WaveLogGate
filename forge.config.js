const { execSync } = require('child_process');

// Get repository info dynamically from git remote
function getRepoInfo() {
	// In GitHub Actions, use the GITHUB_REPOSITORY environment variable
	if (process.env.GITHUB_REPOSITORY) {
		const [owner, name] = process.env.GITHUB_REPOSITORY.split('/');
		return { owner, name };
	}

	// Fallback: read from git remote
	try {
		const remoteUrl = execSync('git remote get-url origin', { encoding: 'utf8' }).trim();
		// Handle various git URL formats:
		// https://github.com/owner/repo.git
		// git@github.com:owner/repo.git
		const match = remoteUrl.match(/(?:github\.com[/:]|github\.com\/)([^/]+)\/([^/]+?)(?:\.git)?$/i);
		if (match) {
			return { owner: match[1], name: match[2] };
		}
	} catch (e) {
		console.log('Could not determine repository from git remote:', e.message);
	}

	// Final fallback to defaults
	return { owner: 'wavelog', name: 'WaveLogGate' };
}

const repoInfo = getRepoInfo();

module.exports = {
	packagerConfig: {
		// set config executableName
		executableName: "wlgate",
		icon: './icon',
		asar: true,
		// Windows updater configuration
		productName: "WaveLogGate",
		win32Metadata: {
			companyName: "DJ7NT"
		}
	},
	publishers: [
		{
			name: '@electron-forge/publisher-github',
			config: {
				repository: {
					owner: repoInfo.owner,
					name: repoInfo.name
				},
				prerelease: false
			}
		}
	],
	rebuildConfig: {},
	makers: [
		// Use NSIS instead of Squirrel for better electron-updater compatibility
		{
			name: '@electron-forge/maker-nsis',
			config: {
				icon: "./icon",
				createDesktopShortcut: true,
				createStartMenuShortcut: true,
				perMachine: false
			},
		},
		{
			name: '@electron-forge/maker-dmg',
			config: { format: 'UDZO' },
			platforms: ['darwin'],
			arch: ['x64','arm64'],
		},
		{
			name: '@electron-forge/maker-zip',
			platforms: ['darwin'],
		},
		{
			name: '@electron-forge/maker-deb',
			config: { "bin":"wlgate" },
			arch: ['x86','armv7l']
		},
	],
	plugins: [
		{
			name: '@electron-forge/plugin-auto-unpack-natives',
			config: {},
		},
	],
};
