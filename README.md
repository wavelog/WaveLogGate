# CAT and WSJT-X Bridge for Wavelog

#### Prerequisites
* [FLRig](http://www.w1hkj.com/) properly SetUp to your TRX
* WaveLog-Account on any WaveLog instance

#### Setup:
1. Download Binary (at Releases)
2. Start Binary (for Windows: Start Binary and after that you have a NEW Binary. One can delete the old one)
3. Fill in informations:
  * WAVELog-URL including index.php (if you setted it up with that)
  * API-Key (from Wavelog / Right Menu / API-Keys)
  * Station-ID (from Wavelog / Right Menu / See Stationlocations / small badge with station-ID)
4. If you're running FLRig on the same machine put 127.0.0.1 to FLRig-Host and 12345 to FLRig Port and enable it.
5. Click "Test" - Button becomes green if working, Red with detailled issue below, when faulty.
6. Click "Save" if everything is okay

#### WSJT-X (and derivates) SetUp:
Go To WSJT-X-Settings // Reporting
Enable "Secondary UDP Server" like shown in the picture. Do NOT set "UDP Server" (above) to the same Port!

![image](https://github.com/wavelog/waveloggate/assets/1410708/7238b193-c589-4ae3-97f8-eae506965dff)


#### Features
* When clicking on a spot in WaveLog-Bandmap your TRX with QSY to the Spot.
* If you log a (non WSJT-X) QSO first go to "Stations Tab" and chose "WSJTX 2 WL" as Radio. After that Band/Mode/QRG will be automatically taken from your Rig into the QSO-Fields
* When clicking the loupe at Live-QSO/Post-QSO Wavelog will automaticly lookup the Spot behind the QRG (if there's a spot)

Enjoy
