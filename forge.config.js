module.exports = {
	packagerConfig: {
		// set config executableName
		executableName: "wlgate",
		icon: './icon',
		asar: true,
	},
	publishers: [
		{
			name: '@electron-forge/publisher-github',
			config: {
				repository: {
					owner: 'int2001',
					name: 'WaveLogGate'
				},
				prerelease: false
			}
		}
	],
	rebuildConfig: {},
	makers: [
		{
			name: '@electron-forge/maker-squirrel',
			config: { icon: "./icon.png", maintainer: 'DJ7NT', loadingGif: "loading.gif", name: "WLGate_by_DJ7NT" },
		},
		{
			name: '@electron-forge/maker-dmg',
			config: { format: 'UDZO' },
			platforms: ['darwin'],
			arch: ['x64','arm64'],
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
