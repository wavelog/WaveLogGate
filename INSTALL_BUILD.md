# CAT and WSJT-X Bridge for Wavelog

## SetUp DevEnv

### on a mac
#### Prerequisites
* XCode
* brew

#### Setup:
1. go to CLI and type `brew install node`
2. clone repo: `git clone https://github.com/wavelog/WaveLogGate.git`
3. change to repo-directory
4. type: `npm install`
5. type: `npm install -g electron-forge`

#### Usage:
* `npm run start` for starting the App in dev-mode

#### Build/Compile:
* `npm run make` - after successful build the binary will appear in the subfolder "out"
