module.exports = {
	packagerConfig: {
		// set config executableName
		executableName: "wlgate",
		icon: './favicon.ico',
		asar: true,
	},
	publishers: [
		{
			name: '@electron-forge/publisher-github',
			config: {
				repository: {
					owner: 'HB9HIL',
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
			config: { icon: "./favicon.ico", maintainer: 'DJ7NT', loadingGif: "loading.gif", name: "WLGate_by_DJ7NT" },
		},
		{
			name: '@electron-forge/maker-dmg',
			config: { format: 'UDZO' },
			platforms: ['darwin'],
			arch: ['universal'],
		},
		{
			name: '@electron-forge/maker-deb',
			config: { "bin":"wlgate" },
			arch: ['x86']
		},
	],
	plugins: [
		{
			name: '@electron-forge/plugin-auto-unpack-natives',
			config: {},
		},
	],
};
