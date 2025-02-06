const {ipcRenderer} = require('electron');

var cfg = {};

const bt_save=document.querySelector("#save");

$(document).ready(function() {
    cfg=ipcRenderer.sendSync("get_config", '');

    $("#hamlib_host").val(cfg.hamlib_host);
    $("#hamlib_port").val(cfg.hamlib_port);
    $("#hamlib_ena").prop("checked", cfg.hamlib_ena);

    bt_save.addEventListener('click', () => {
        cfg.hamlib_host=$("#hamlib_host").val().trim();
        cfg.hamlib_port=$("#hamlib_port").val().trim();
        cfg.hamlib_ena=$("#hamlib_ena").is(':checked');

        if ($("#hamlib_ena").is(':checked') || cfg.flrig_ena){cfg.flrig_ena = false;}

        x=ipcRenderer.sendSync("set_config", cfg);
        console.log(x);

    });
});
