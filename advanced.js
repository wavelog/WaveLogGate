const {ipcRenderer} = require('electron');

var cfg = {};

const bt_save=document.querySelector("#save");

$(document).ready(function() {
    cfg=ipcRenderer.sendSync("get_config", '');

    $("#hamlib_host").val(cfg.profiles[cfg.profile].hamlib_host ?? '');
    $("#hamlib_port").val(cfg.profiles[cfg.profile].hamlib_port ?? '');
    $("#hamlib_ena").prop("checked", cfg.profiles[cfg.profile].hamlib_ena ?? '');
    $("#ignore_pwr").prop("checked", cfg.profiles[cfg.profile].ignore_pwr ?? '');

    bt_save.addEventListener('click', async () => {
        cfg=await ipcRenderer.sendSync("get_config", '');
        cfg.profiles[cfg.profile].hamlib_host=$("#hamlib_host").val().trim();
        cfg.profiles[cfg.profile].hamlib_port=$("#hamlib_port").val().trim();
        cfg.profiles[cfg.profile].hamlib_ena=$("#hamlib_ena").is(':checked');
        cfg.profiles[cfg.profile].ignore_pwr=$("#ignore_pwr").is(':checked');

        if ($("#hamlib_ena").is(':checked') && cfg.profiles[cfg.profile].flrig_ena){cfg.profiles[cfg.profile].flrig_ena = false;}

        x=ipcRenderer.sendSync("set_config", cfg);
        // console.log(x);

    });
});
