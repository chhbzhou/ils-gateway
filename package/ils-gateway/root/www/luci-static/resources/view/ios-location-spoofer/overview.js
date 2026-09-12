'use strict';
'require view';
'require uci';
'require fs';
'require rpc';
'require ui';

var callHostHints = rpc.declare({
    object: 'luci-rpc',
    method: 'getHostHints',
    expect: { '': {} }
});

        var assetRevision = '39';

var ensureStylesheet = function() {
    var id = 'ils-location-spoofer-style';
    if (document.getElementById(id))
        return;
    document.head.appendChild(E('link', {
        'id': id,
        'rel': 'stylesheet',
        'href': L.resource('view/ios-location-spoofer/overview.css') + '?v=' + assetRevision
    }));
};

var loadScript = function(id, file, globalName) {
    if (window[globalName])
        return Promise.resolve();

    return new Promise(function(resolve, reject) {
        var existing = document.getElementById(id);
        if (existing) {
            existing.addEventListener('load', resolve, { once: true });
            existing.addEventListener('error', reject, { once: true });
            return;
        }

        var script = E('script', {
            'id': id,
            'src': L.resource('view/ios-location-spoofer/' + file) + '?v=' + assetRevision
        });
        script.addEventListener('load', resolve, { once: true });
        script.addEventListener('error', reject, { once: true });
        document.head.appendChild(script);
    });
};

return view.extend({
    handleSaveApply: null,
    handleSave: null,
    handleReset: null,

    load: function() {
        ensureStylesheet();
        return Promise.all([
            uci.load('ios-location-spoofer'),
            callHostHints().catch(function() { return {}; }),
            fs.exec('/usr/bin/locspoof-status', ['status']).then(function(result) {
                try { return JSON.parse(result.stdout || '{}'); } catch (error) { return {}; }
            }).catch(function() { return {}; }),
            fs.exec('/usr/bin/locspoof-status', ['health']).then(function(result) {
                try { return JSON.parse(result.stdout || '{}').result || {}; } catch (error) { return {}; }
            }).catch(function() { return {}; }),
            fs.exec('/usr/bin/locspoof-status', ['online']).then(function(result) {
                try { return JSON.parse(result.stdout || '{}'); } catch (error) { return {}; }
            }).catch(function() { return {}; }),
            fs.exec('/usr/bin/locspoof-status', ['activity']).then(function(result) {
                try { return JSON.parse(result.stdout || '{}'); } catch (error) { return {}; }
            }).catch(function() { return {}; }),
            loadScript('ils-lucide-script', 'lucide.min.js', 'lucide'),
            loadScript('ils-qrcode-script', 'qrcode.min.js', 'QRCode')
        ]);
    },

    render: function(values) {
        var config = 'ios-location-spoofer';
        var hostHints = values[1] || {};
        var state = values[2] || {};
        var health = values[3] || {};
        var onlineState = values[4] || {};
        var persistedActivity = values[5] || {};
        var activityByIP = Object.assign({}, persistedActivity.by_ip || persistedActivity, health.last_modified_by_ip || {});
        var activityByMAC = Object.assign({}, persistedActivity.by_mac || {}, health.last_modified_by_mac || {});
        var profiles = uci.sections(config, 'profile');
        var devices = uci.sections(config, 'device');
        var main = uci.get(config, 'main') || {};
        var activeProfile = profiles.find(function(profile) { return profile.active === '1'; }) || profiles[0] || {};
        var root;
        var menu;

        var icon = function(name) {
            return E('i', { 'data-lucide': name, 'aria-hidden': 'true' });
        };
        var renderIcons = function() {
            window.setTimeout(function() {
                if (window.lucide)
                    window.lucide.createIcons({ attrs: { width: 16, height: 16, 'stroke-width': 1.8 } });
            }, 0);
        };
        var notifyError = function(error) {
            ui.addNotification(null, E('p', error.message || String(error)), 'error');
        };
        var certificateURL = function() {
            var host = window.location.hostname;
            if (host.indexOf(':') >= 0)
                host = '[' + host + ']';
            return 'http://' + host + ':10445/ca.mobileconfig';
        };
        var applyAndConfirm = function(timeout) {
            var deadline = Date.now() + timeout * 1000;
            return uci.callApply(timeout, true).then(function(result) {
                if (result !== 0)
                    throw new Error(_('应用配置失败：') + result);

                return new Promise(function(resolve, reject) {
                    var confirm = function() {
                        uci.callConfirm().then(function(confirmResult) {
                            if (confirmResult === 0)
                                return resolve();
                            if (Date.now() < deadline)
                                return window.setTimeout(confirm, 250);
                            reject(new Error(_('确认配置失败，系统已自动回滚')));
                        }).catch(function(error) {
                            if (Date.now() < deadline)
                                return window.setTimeout(confirm, 250);
                            reject(error);
                        });
                    };
                    window.setTimeout(confirm, 500);
                });
            });
        };
        var commitChanges = function() {
            return uci.save().then(function() {
                return uci.changes();
            }).then(function(changes) {
                if (!changes || Object.keys(changes).length === 0)
                    return;
                return applyAndConfirm(10);
            });
        };
        var waitForServiceState = function(expectedEnabled) {
            var deadline = Date.now() + 12000;
            return new Promise(function(resolve, reject) {
                var check = function() {
                    fs.exec('/usr/bin/locspoof-status', ['status']).then(function(result) {
                        var current = {};
                        try { current = JSON.parse(result.stdout || '{}'); } catch (error) {}
                        var ready = expectedEnabled
                            ? current.desired_enabled && current.effective_enabled
                            : !current.desired_enabled && !current.effective_enabled;
                        if (ready)
                            return resolve();
                        if (Date.now() < deadline)
                            return window.setTimeout(check, 300);
                        reject(new Error(expectedEnabled ? _('定位服务启动超时') : _('定位服务停止超时')));
                    }).catch(function(error) {
                        if (Date.now() < deadline)
                            return window.setTimeout(check, 300);
                        reject(error);
                    });
                };
                window.setTimeout(check, 400);
            });
        };
        var runAction = function(action, argument) {
            var args = [action];
            if (argument)
                args.push(argument);
            return fs.exec('/usr/bin/locspoof-service', args).then(function(result) {
                if (result.code)
                    throw new Error(result.stderr || _('操作失败'));
                if (action === 'start' || action === 'stop' || action === 'restart' || action === 'activate_profile')
                    return waitForServiceState(uci.get(config, 'main', 'enabled') === '1');
            }).then(function() {
                window.location.reload();
            });
        };
        var saveAndRun = function(action, argument) {
            return commitChanges().then(function() {
                return runAction(action, argument);
            }).catch(function(error) {
                uci.unload(config);
                notifyError(error);
                throw error;
            });
        };
        var setOption = function(sectionId, option, value) {
            if (value == null || value === '')
                uci.unset(config, sectionId, option);
            else
                uci.set(config, sectionId, option, String(value));
        };
        var splitList = function(value) {
            return String(value || '').split(/[\n,]+/).map(function(item) {
                return item.trim();
            }).filter(Boolean);
        };
        var setList = function(sectionId, option, value) {
            var list = splitList(value);
            if (list.length)
                uci.set(config, sectionId, option, list);
            else
                uci.unset(config, sectionId, option);
        };
        var validNumber = function(value, min, max) {
            var number = Number(value);
            return value !== '' && Number.isFinite(number) && number >= min && number <= max;
        };
        var validUnsigned = function(value) {
            return value === '' || /^[0-9]+$/.test(value);
        };
        var validMac = function(value) {
            return /^([0-9A-F]{2}:){5}[0-9A-F]{2}$/i.test(value);
        };
        var validIP = function(value) {
            if (!value)
                return true;
            if (/^[0-9A-Fa-f:]+$/.test(value) && value.indexOf(':') >= 0)
                return true;
            var parts = value.split('.');
            return parts.length === 4 && parts.every(function(part) {
                return /^[0-9]{1,3}$/.test(part) && Number(part) <= 255;
            });
        };
        var button = function(label, iconName, className, handler, attributes) {
            var attrs = Object.assign({
                'type': 'button',
                'class': className || 'ils-button',
                'click': handler
            }, attributes || {});
            return E('button', attrs, [icon(iconName), label]);
        };
        var iconButton = function(label, iconName, handler, className, disabled) {
            return E('button', {
                'type': 'button',
                'class': className || 'ils-link-button',
                'aria-label': label,
                'title': label,
                'disabled': disabled ? '' : null,
                'click': handler
            }, [icon(iconName)]);
        };
        var switchControl = function(label, checked, handler) {
            var input = E('input', { 'type': 'checkbox', 'checked': checked ? '' : null });
            input.addEventListener('change', handler);
            return E('label', { 'class': 'ils-switch', 'title': label }, [
                input,
                E('span', { 'class': 'ils-switch-track' })
            ]);
        };
        var field = function(label, input, full) {
            return E('div', { 'class': 'ils-field' + (full ? ' ils-field-full' : '') }, [
                E('label', { 'for': input.id }, label),
                input
            ]);
        };
        var textInput = function(id, value, type, placeholder) {
            return E('input', {
                'id': id,
                'type': type || 'text',
                'value': value == null ? '' : value,
                'placeholder': placeholder || ''
            });
        };
        var textarea = function(id, value, placeholder) {
            return E('textarea', { 'id': id, 'placeholder': placeholder || '' }, value || '');
        };
        var closeOverlay = function(overlay) {
            if (overlay && overlay.parentNode)
                overlay.parentNode.removeChild(overlay);
        };
        var openModal = function(options) {
            var errorNode = E('div', { 'class': 'ils-form-error' });
            var overlay = E('div', { 'class': 'ils-modal-overlay' });
            var cancelButton = E('button', {
                'type': 'button',
                'class': 'ils-button',
                'click': function() { closeOverlay(overlay); }
            }, options.cancelLabel || _('取消'));
            var saveButton;
            var footerChildren = [];

            if (options.dangerLabel && options.onDanger) {
                footerChildren.push(E('button', {
                    'type': 'button',
                    'class': 'ils-button ils-link-button-danger',
                    'click': function() {
                        Promise.resolve().then(options.onDanger).catch(function(error) {
                            errorNode.textContent = error.message || String(error);
                            errorNode.classList.add('is-visible');
                        });
                    }
                }, [icon('trash-2'), options.dangerLabel]));
                footerChildren.push(E('span', { 'style': 'flex:1' }));
            }

            footerChildren.push(cancelButton);
            if (options.onSave) {
                saveButton = E('button', {
                    'type': 'button',
                    'class': 'ils-button ils-button-primary',
                    'click': function() {
                        saveButton.disabled = true;
                        errorNode.classList.remove('is-visible');
                        Promise.resolve().then(options.onSave).then(function() {
                            closeOverlay(overlay);
                        }).catch(function(error) {
                            saveButton.disabled = false;
                            errorNode.textContent = error.message || String(error);
                            errorNode.classList.add('is-visible');
                        });
                    }
                }, [icon(options.saveIcon || 'check'), options.saveLabel || _('保存')]);
                footerChildren.push(saveButton);
            }

            var modal = E('section', {
                'class': 'ils-modal' + (options.wide ? ' ils-modal-wide' : ''),
                'role': 'dialog',
                'aria-modal': 'true'
            }, [
                E('div', { 'class': 'ils-modal-head' }, [
                    E('h2', {}, options.title),
                    iconButton(_('关闭'), 'x', function() { closeOverlay(overlay); }, 'ils-icon-button')
                ]),
                E('div', { 'class': 'ils-modal-body' }, [options.body, errorNode]),
                E('div', { 'class': 'ils-modal-footer' }, footerChildren)
            ]);

            overlay.appendChild(modal);
            root.appendChild(overlay);
            renderIcons();
            return overlay;
        };
        var confirmAction = function(title, message, buttonLabel, callback) {
            return openModal({
                title: title,
                body: E('p', { 'class': 'ils-dialog-copy' }, message),
                saveLabel: buttonLabel,
                saveIcon: 'check',
                onSave: callback
            });
        };
        var hostHint = function(mac) {
            var normalized = String(mac || '').toUpperCase();
            var key = Object.keys(hostHints).find(function(candidate) {
                return candidate.toUpperCase() === normalized;
            });
            return key ? hostHints[key] || {} : {};
        };
        var recentAddresses = function(mac) {
            var hint = hostHint(mac);
            return (hint.ipaddrs || []).concat(hint.ip6addrs || []);
        };
        var onlineMACs = (onlineState.mac || []).map(function(value) { return String(value).toUpperCase(); });
        var onlineIPs = (onlineState.ipv4 || []).concat(onlineState.ipv6 || []).map(String);
        var deviceIsOnline = function(device, addresses) {
            var mac = String(device.mac || '').toUpperCase();
            if (mac && onlineMACs.indexOf(mac) >= 0)
                return true;
            return addresses.concat(device.ip || []).some(function(address) {
                return address && onlineIPs.indexOf(String(address)) >= 0;
            });
        };
        var lastModified = function(device, addresses) {
            var latest = null;
            var mac = String(device.mac || '').toLowerCase();
            var macValue = mac ? activityByMAC[mac] || activityByMAC[mac.toUpperCase()] : null;
            var macParsed = macValue ? new Date(macValue) : null;
            if (macParsed && !isNaN(macParsed.getTime()))
                latest = macParsed;
            addresses.concat(device.ip || []).forEach(function(address) {
                var value = activityByIP[address];
                var parsed = value ? new Date(value) : null;
                if (parsed && !isNaN(parsed.getTime()) && (!latest || parsed > latest))
                    latest = parsed;
            });
            return latest;
        };
        var formatDateTime = function(value) {
            if (!value)
                return _('暂无记录');
            var pad = function(number) { return String(number).padStart(2, '0'); };
            return value.getFullYear() + '-' + pad(value.getMonth() + 1) + '-' + pad(value.getDate()) + ' ' +
                pad(value.getHours()) + ':' + pad(value.getMinutes()) + ':' + pad(value.getSeconds());
        };
        var deviceIdentity = function(device, modified) {
            var hint = hostHint(device.mac);
            var detectedName = String(hint.name || hint.hostname || '').toLowerCase();
            if (detectedName.indexOf('iphone') >= 0)
                return { label: 'iPhone', icon: 'smartphone' };
            if (detectedName.indexOf('ipad') >= 0)
                return { label: 'iPad', icon: 'tablet' };
            if (/macbook|imac|mac-mini|mac mini/.test(detectedName))
                return { label: 'Mac', icon: 'laptop' };
            if (/android|xiaomi|redmi|huawei|honor|oppo|vivo|oneplus|pixel|samsung/.test(detectedName))
                return { label: _('Android 手机'), icon: 'smartphone' };
            if (/windows|desktop|laptop|pc-/.test(detectedName))
                return { label: _('电脑'), icon: 'monitor' };
            if (modified)
                return { label: _('Apple 设备'), icon: 'smartphone' };
            return { label: _('未知设备'), icon: 'circle-help' };
        };

        var openSettings = function() {
            var ipv6Block = E('input', { 'type': 'checkbox', 'checked': main.ipv6_block === '1' ? '' : null });
            var upstream = textInput('ils-upstream', main.upstream_socks5 || '', 'text', 'host:port');
            var interfaces = textarea('ils-interfaces', Array.isArray(main.lan_interface) ? main.lan_interface.join('\n') : (main.lan_interface || 'br-lan'));
            var maxConnections = textInput('ils-max-connections', main.max_connections || '32', 'number');
            var maxBody = textInput('ils-max-body', main.max_body_bytes || '1048576', 'number');
            var maxBuffered = textInput('ils-max-buffered', main.max_buffered_bytes || '16777216', 'number');
            var hosts = textarea('ils-hosts', Array.isArray(main.wloc_host) ? main.wloc_host.join('\n') : (main.wloc_host || ''));
            var ipv6Switch = E('label', { 'class': 'ils-switch' }, [ipv6Block, E('span', { 'class': 'ils-switch-track' })]);
            var body = E('div', { 'class': 'ils-form-grid' }, [
                E('div', { 'class': 'ils-toggle-field' }, [
                    E('div', { 'class': 'ils-setting-copy' }, [E('strong', {}, _('阻断授权设备 IPv6')), E('span', {}, _('避免 IPv6 绕过定位处理'))]),
                    ipv6Switch
                ]),
                field(_('上游 SOCKS5 代理'), upstream),
                field(_('最大并发连接数'), maxConnections),
                field(_('单次响应最大字节数'), maxBody),
                field(_('全局缓冲区上限'), maxBuffered),
                field(_('局域网接口（每行一个）'), interfaces, true),
                field(_('Apple 定位服务域名（每行一个）'), hosts, true)
            ]);

            openModal({
                title: _('服务设置'),
                body: body,
                wide: true,
                saveLabel: _('保存设置'),
                saveIcon: 'check',
                onSave: function() {
                    var interfaceList = splitList(interfaces.value);
                    var hostList = splitList(hosts.value);
                    if (!interfaceList.length || interfaceList.some(function(value) { return !/^[A-Za-z0-9_.:-]{1,15}$/.test(value); }))
                        throw new Error(_('局域网接口格式不正确'));
                    if (hostList.some(function(value) { return !/^[A-Za-z0-9.-]+$/.test(value); }))
                        throw new Error(_('Apple 定位服务域名格式不正确'));
                    if (![maxConnections, maxBody, maxBuffered].every(function(input) { return validUnsigned(input.value) && Number(input.value) > 0; }))
                        throw new Error(_('连接数和字节数必须为正整数'));

                    setOption('main', 'ipv6_block', ipv6Block.checked ? '1' : '0');
                    setOption('main', 'upstream_socks5', upstream.value.trim());
                    setList('main', 'lan_interface', interfaces.value);
                    setOption('main', 'max_connections', maxConnections.value);
                    setOption('main', 'max_body_bytes', maxBody.value);
                    setOption('main', 'max_buffered_bytes', maxBuffered.value);
                    setList('main', 'wloc_host', hosts.value);
                    var requiresRestart = upstream.value.trim() !== (main.upstream_socks5 || '') ||
                        maxConnections.value !== (main.max_connections || '32') ||
                        maxBody.value !== (main.max_body_bytes || '1048576') ||
                        maxBuffered.value !== (main.max_buffered_bytes || '16777216');
                    return saveAndRun(requiresRestart ? 'restart' : 'reload');
                }
            });
        };

        var openProfileEditor = function(profile) {
            var sectionId = profile && profile['.name'];
            var name = textInput('ils-profile-name', profile && profile.name || '', 'text', _('例如：香港'));
            var note = textInput('ils-profile-note', profile && profile.note || '', 'text', _('例如：出差测试'));
            var latitude = textInput('ils-profile-latitude', profile && profile.latitude || '', 'number');
            var longitude = textInput('ils-profile-longitude', profile && profile.longitude || '', 'number');
            latitude.step = 'any';
            longitude.step = 'any';
            var randomInteger = function(min, max) { return String(Math.floor(Math.random() * (max - min + 1)) + min); };
            var altitude = textInput('ils-profile-altitude', profile && profile.altitude || '0', 'number');
            var horizontal = textInput('ils-profile-horizontal', profile && Number(profile.horizontal_accuracy) > 0 ? profile.horizontal_accuracy : randomInteger(20, 40), 'number');
            var vertical = textInput('ils-profile-vertical', profile && Number(profile.vertical_accuracy) > 0 ? profile.vertical_accuracy : randomInteger(10, 25), 'number');
            var randomRadius = textInput('ils-profile-random-radius', profile && profile.random_radius || '0', 'number');
            [altitude, horizontal, vertical, randomRadius].forEach(function(input) { input.step = 'any'; });
            var altitudeMessage = E('span', { 'class': 'ils-inline-message' });
            var altitudeButton = button(_('补全'), 'mountain', 'ils-button ils-input-action-button', function() {
                altitudeMessage.className = 'ils-inline-message';
                altitudeMessage.textContent = '';
                if (!validNumber(latitude.value, -90, 90) || !validNumber(longitude.value, -180, 180)) {
                    altitudeMessage.className = 'ils-inline-message is-error';
                    altitudeMessage.textContent = _('请先填写有效的经纬度');
                    return;
                }
                altitudeButton.disabled = true;
                altitudeButton.setAttribute('aria-busy', 'true');
                fs.exec('/usr/bin/locspoof-elevation', [latitude.value, longitude.value]).then(function(result) {
                    if (result.code)
                        throw new Error(result.stderr || _('海拔查询失败'));
                    var payload = JSON.parse(result.stdout || '{}');
                    if (!Number.isFinite(Number(payload.elevation)))
                        throw new Error(_('海拔服务返回了无效数据'));
                    altitude.value = String(Number(payload.elevation));
                    altitudeMessage.className = 'ils-inline-message is-success';
                    altitudeMessage.textContent = _('已补全');
                }).catch(function(error) {
                    altitudeMessage.className = 'ils-inline-message is-error';
                    altitudeMessage.textContent = error.message || _('海拔查询失败');
                }).finally(function() {
                    altitudeButton.disabled = false;
                    altitudeButton.removeAttribute('aria-busy');
                });
            });
            var altitudeField = E('div', { 'class': 'ils-field' }, [
                E('label', { 'for': altitude.id }, _('海拔')),
                E('div', { 'class': 'ils-input-action' }, [altitude, altitudeButton]),
                altitudeMessage
            ]);
            var body = E('div', { 'class': 'ils-form-grid' }, [
                field(_('名称'), name),
                field(_('备注'), note),
                field(_('纬度'), latitude),
                field(_('经度'), longitude),
                altitudeField,
                field(_('水平精度'), horizontal),
                field(_('垂直精度'), vertical),
                field(_('随机范围（米）'), randomRadius)
            ]);

            openModal({
                title: sectionId ? _('编辑定位点') : _('添加定位点'),
                body: body,
                saveLabel: sectionId ? _('保存定位点') : _('添加定位点'),
                saveIcon: 'map-pin-check',
                onSave: function() {
                    if (!name.value.trim())
                        throw new Error(_('请填写定位点名称'));
                    if (!validNumber(latitude.value, -90, 90) || !validNumber(longitude.value, -180, 180))
                        throw new Error(_('纬度或经度超出有效范围'));
                    if (!validNumber(altitude.value, -100000, 100000) || !validNumber(horizontal.value, 0, 100000) || !validNumber(vertical.value, 0, 100000) || !validNumber(randomRadius.value, 0, 100000))
                        throw new Error(_('精度或海拔格式不正确'));

                    var sid = sectionId || uci.add(config, 'profile');
                    setOption(sid, 'name', name.value.trim());
                    setOption(sid, 'note', note.value.trim());
                    setOption(sid, 'latitude', latitude.value);
                    setOption(sid, 'longitude', longitude.value);
                    setOption(sid, 'altitude', altitude.value);
                    setOption(sid, 'horizontal_accuracy', horizontal.value);
                    setOption(sid, 'vertical_accuracy', vertical.value);
                    setOption(sid, 'random_radius', randomRadius.value);
                    if (!sectionId)
                        setOption(sid, 'active', profiles.length ? '0' : '1');
                    return saveAndRun('reload');
                }
            });
        };

        var openDeviceEditor = function(device) {
            var sectionId = device && device['.name'];
            var name = textInput('ils-device-name', device && device.name || '', 'text', _('例如：我的 iPhone'));
            var mac = textInput('ils-device-mac', device && device.mac || '', 'text', 'AA:BB:CC:DD:EE:FF');
            var ip = textInput('ils-device-ip', device && device.ip || '', 'text', '192.168.1.100');
            var note = textInput('ils-device-note', device && device.note || '', 'text', _('例如：家人设备'));
            var enabled = E('input', { 'type': 'checkbox', 'checked': !device || device.enabled !== '0' ? '' : null });
            var dataListId = 'ils-host-hints-' + Math.random().toString(16).slice(2);
            mac.setAttribute('list', dataListId);
            var dataList = E('datalist', { 'id': dataListId }, Object.keys(hostHints).sort().map(function(address) {
                var hint = hostHints[address] || {};
                var ips = (hint.ipaddrs || []).concat(hint.ip6addrs || []).slice(0, 2);
                return E('option', { 'value': address, 'label': ips.join(', ') });
            }));
            var macField = field(_('Wi-Fi MAC'), mac);
            macField.appendChild(dataList);
            var body = E('div', {}, [
                E('div', { 'class': 'ils-settings-grid ils-settings-grid-single', 'style': 'margin-bottom:12px' }, [
                    E('div', { 'class': 'ils-setting-row' }, [
                        E('div', { 'class': 'ils-setting-copy' }, [E('strong', {}, _('启用此设备')), E('span', {}, _('仅在定位服务开启时生效；停用后不再处理该设备定位'))]),
                        E('label', { 'class': 'ils-switch' }, [enabled, E('span', { 'class': 'ils-switch-track' })])
                    ])
                ]),
                E('div', { 'class': 'ils-form-grid' }, [
                    field(_('设备名称'), name),
                    field(_('备注'), note),
                    macField,
                    field(_('固定 IP（可选）'), ip)
                ])
            ]);

            openModal({
                title: sectionId ? _('编辑设备') : _('添加设备'),
                body: body,
                saveLabel: sectionId ? _('保存设备') : _('添加设备'),
                saveIcon: 'smartphone',
                dangerLabel: sectionId ? _('删除设备') : null,
                onDanger: sectionId ? function() {
                    var deviceName = name.value.trim() || sectionId;
                    return confirmAction(
                        _('删除设备'),
                        _('确定删除“%s”吗？该设备会立即停止使用模拟定位。').format(deviceName),
                        _('确认删除'),
                        function() {
                            uci.remove(config, sectionId);
                            return saveAndRun('reload');
                        }
                    );
                } : null,
                onSave: function() {
                    var normalizedMac = mac.value.trim().toUpperCase();
                    var normalizedIP = ip.value.trim();
                    if (!name.value.trim())
                        throw new Error(_('请填写设备名称'));
                    if (!normalizedMac && !normalizedIP)
                        throw new Error(_('请至少填写 Wi-Fi MAC 或固定 IP'));
                    if (normalizedMac && !validMac(normalizedMac))
                        throw new Error(_('Wi-Fi MAC 格式不正确'));
                    if (!validIP(normalizedIP))
                        throw new Error(_('固定 IP 格式不正确'));

                    var sid = sectionId || uci.add(config, 'device');
                    setOption(sid, 'enabled', enabled.checked ? '1' : '0');
                    setOption(sid, 'name', name.value.trim());
                    setOption(sid, 'note', note.value.trim());
                    setOption(sid, 'mac', normalizedMac);
                    setOption(sid, 'ip', normalizedIP);
                    return saveAndRun('reload');
                }
            });
        };

        var openCertificate = function() {
            var url = certificateURL();
            var urlInput = textInput('ils-certificate-url', url, 'text');
            urlInput.readOnly = true;
            var qrBox = E('div', { 'class': 'ils-qr-box', 'aria-label': _('证书安装二维码') });
            var copyButton = iconButton(_('复制安装地址'), 'copy', function() {
                var copy = navigator.clipboard && navigator.clipboard.writeText
                    ? navigator.clipboard.writeText(url)
                    : Promise.reject(new Error('clipboard unavailable'));
                copy.then(function() {
                    copyButton.replaceChildren(icon('check'));
                    renderIcons();
                }).catch(function() {
                    urlInput.focus();
                    urlInput.select();
                    document.execCommand('copy');
                });
            }, 'ils-icon-button');
            var body = E('div', { 'class': 'ils-certificate-layout' }, [
                qrBox,
                E('div', { 'class': 'ils-certificate-copy' }, [
                    E('h3', {}, _('iPhone / iPad')),
                    E('ol', { 'class': 'ils-install-steps' }, [
                        E('li', {}, _('下载描述文件')),
                        E('li', {}, _('安装描述文件')),
                        E('li', {}, _('信任根证书'))
                    ]),
                    E('div', { 'class': 'ils-field' }, [E('label', { 'for': urlInput.id }, _('安装地址'))]),
                    E('div', { 'class': 'ils-url-row' }, [urlInput, copyButton]),
                    E('a', {
                        'class': 'ils-button ils-button-primary',
                        'href': url,
                        'target': '_blank',
                        'rel': 'noopener'
                    }, [icon('download'), _('在此设备下载')])
                ])
            ]);
            openModal({ title: _('安装 iPhone CA 证书'), body: body, wide: true });
            if (window.QRCode)
                new window.QRCode(qrBox, { text: url, width: 184, height: 184, correctLevel: window.QRCode.CorrectLevel.M });
        };

        var diagnosticGroup = function(title, rows) {
            var children = [];
            rows.forEach(function(row) {
                children.push(E('dt', {}, row[0]));
                children.push(E('dd', { 'class': row[2] ? 'ils-ok' : '' }, row[1]));
            });
            return E('section', { 'class': 'ils-diagnostic-group' }, [
                E('h3', {}, title),
                E('dl', { 'class': 'ils-diagnostic-list' }, children)
            ]);
        };
        var openDiagnostics = function() {
            var overlay = E('div', { 'class': 'ils-drawer-overlay' });
            var drawer = E('aside', { 'class': 'ils-drawer', 'aria-label': _('技术诊断') }, [
                E('div', { 'class': 'ils-drawer-head' }, [
                    E('h2', {}, _('技术诊断')),
                    iconButton(_('关闭'), 'x', function() { closeOverlay(overlay); }, 'ils-icon-button')
                ]),
                diagnosticGroup(_('运行状态'), [
                    [_('配置状态'), main.enabled === '1' ? _('已启用') : _('已停用')],
                    [_('守护进程'), health.data_plane_healthy ? _('运行中') : _('已停止'), !health.data_plane_healthy && main.enabled !== '1'],
                    [_('流量拦截'), state.nft_applied ? _('已启用') : _('未启用'), !state.nft_applied && main.enabled !== '1'],
                    [_('规则校验'), state.nft_verified ? _('通过') : _('未通过'), !!state.nft_verified]
                ]),
                diagnosticGroup(_('最近统计'), [
                    [_('WLoc 请求'), String(health.target_requests || 0)],
                    [_('已修改响应'), String(health.response_modified || 0)],
                    [_('最近修改位置点'), String(health.last_locations_modified || 0)],
                    [_('活动连接'), String(health.active_connections || 0)]
                ]),
                diagnosticGroup(_('证书与网络'), [
                    [_('CA 状态'), state.ca_sha256 ? _('有效') : _('不可用'), !!state.ca_sha256],
                    [_('局域网接口'), Array.isArray(main.lan_interface) ? main.lan_interface.join(', ') : (main.lan_interface || 'br-lan')],
                    [_('目标地址'), (state.target_v4_count || 0) + ' IPv4 / ' + (state.target_v6_count || 0) + ' IPv6'],
                    [_('最近错误'), health.last_error || _('无'), !health.last_error]
                ])
            ]);
            overlay.appendChild(drawer);
            root.appendChild(overlay);
            renderIcons();
        };

        var profileRow = function(profile) {
            var sectionId = profile['.name'];
            var active = profile.active === '1';
            var meta = [];
            if (profile.note)
                meta.push(E('span', {}, profile.note));
            if (profile.note)
                meta.push(E('span', { 'class': 'ils-meta-divider' }));
            meta.push(E('code', {}, [profile.latitude || '-', ', ', profile.longitude || '-']));

            return E('article', { 'class': 'ils-compact-row' }, [
                E('div', { 'class': 'ils-compact-main' }, [
                    E('div', { 'class': 'ils-compact-heading' }, [
                        E('span', { 'class': 'ils-primary-text' }, profile.name || sectionId),
                        active ? E('span', { 'class': 'ils-current-label' }, _('当前定位')) : ''
                    ]),
                    E('div', { 'class': 'ils-compact-meta ils-profile-meta' }, meta)
                ]),
                E('div', { 'class': 'ils-compact-actions' }, [
                    active
                        ? iconButton(_('重新应用'), 'map-pin-check', function() {
                            return confirmAction(
                                _('重新应用定位点'),
                                _('将重新加载“%s”并刷新授权设备。').format(profile.name || sectionId),
                                _('重新应用'),
                                function() { return runAction('activate_profile', sectionId); }
                            );
                        })
                        : button(_('使用'), 'map-pin-check', 'ils-link-button', function() {
                            return confirmAction(
                                _('切换定位点'),
                                _('确定切换到“%s”并刷新授权设备吗？').format(profile.name || sectionId),
                                _('确认使用'),
                                function() { return runAction('activate_profile', sectionId); }
                            );
                        }),
                    iconButton(_('编辑'), 'pencil', function() { openProfileEditor(profile); }),
                    iconButton(active ? _('当前定位点不能删除') : _('删除'), 'trash-2', function() {
                        if (active)
                            return;
                        return confirmAction(
                            _('删除定位点'),
                            _('确定删除“%s”吗？此操作会立即生效。').format(profile.name || sectionId),
                            _('确认删除'),
                            function() {
                                uci.remove(config, sectionId);
                                return saveAndRun('reload');
                            }
                        );
                    }, 'ils-link-button ils-link-button-danger', active)
                ])
            ]);
        };

        var deviceRow = function(device) {
            var sectionId = device['.name'];
            var enabled = device.enabled !== '0';
            var addresses = recentAddresses(device.mac);
            var recentIP = addresses[0] || device.ip || '-';
            var online = deviceIsOnline(device, addresses);
            var modified = lastModified(device, addresses);
            var identity = deviceIdentity(device, modified);
            var enabledSwitch = switchControl(enabled ? _('停用设备') : _('启用设备'), enabled, function(event) {
                event.target.disabled = true;
                setOption(sectionId, 'enabled', event.target.checked ? '1' : '0');
                saveAndRun('reload').catch(function() {
                    event.target.checked = !event.target.checked;
                    event.target.disabled = false;
                });
            });
            var meta = [];
            if (device.mac)
                meta.push(E('code', {}, device.mac));
            if (meta.length)
                meta.push(E('span', { 'class': 'ils-meta-divider' }));
            meta.push(E('code', {}, recentIP));
            meta.push(E('span', { 'class': 'ils-meta-divider ils-meta-divider-before-modified' }));
            meta.push(E('span', { 'class': 'ils-last-modified' }, _('最近成功：%s').format(formatDateTime(modified))));

            return E('article', { 'class': 'ils-compact-row' }, [
                E('div', { 'class': 'ils-compact-main' }, [
                    E('div', { 'class': 'ils-compact-heading' }, [
                        E('span', { 'class': 'ils-primary-text' }, device.name || sectionId),
                        E('span', { 'class': 'ils-online-label' + (online ? '' : ' is-offline') }, online ? _('在线') : _('离线')),
                        E('span', { 'class': 'ils-device-type' }, [icon(identity.icon), E('span', {}, identity.label)])
                    ]),
                    E('div', { 'class': 'ils-compact-meta' }, meta)
                ]),
                E('div', { 'class': 'ils-compact-actions' }, [
                    iconButton(_('编辑'), 'pencil', function() { openDeviceEditor(device); }),
                    enabledSwitch
                ])
            ]);
        };

        var effective = !!state.effective_enabled;
        var desired = main.enabled === '1';
        var activeName = activeProfile.name || _('未设置');
        var enabledDevices = devices.filter(function(device) { return device.enabled !== '0'; }).length;
        var serviceTitle = effective ? _('定位服务运行中') : (desired ? _('定位服务启动异常') : _('定位服务已停用'));
        var serviceDetail = effective
            ? _('当前定位点：%s，正在处理 %d 台授权设备').format(activeName, enabledDevices)
            : _('已选定位点：%s，当前不会修改设备定位').format(activeName);
        var serviceSwitch = switchControl(desired ? _('停用定位服务') : _('启用定位服务'), desired, function(event) {
            event.target.disabled = true;
            setOption('main', 'enabled', event.target.checked ? '1' : '0');
            saveAndRun(event.target.checked ? 'start' : 'stop').catch(function() {
                event.target.checked = !event.target.checked;
                event.target.disabled = false;
            });
        });
        var profileList = profiles.length
            ? E('div', { 'class': 'ils-compact-list' }, profiles.map(profileRow))
            : E('div', { 'class': 'ils-empty' }, _('还没有定位点'));
        var deviceList = devices.length
            ? E('div', { 'class': 'ils-compact-list' }, devices.map(deviceRow))
            : E('div', { 'class': 'ils-empty' }, _('还没有授权设备'));

        menu = E('div', { 'class': 'ils-menu' }, [
            E('button', {
                'type': 'button',
                'class': 'ils-menu-item',
                'click': function() {
                    menu.classList.remove('is-open');
                    confirmAction(
                        _('重启定位服务'),
                        _('重启会短暂中断授权设备的定位请求，确定继续吗？'),
                        _('确认重启'),
                        function() { return runAction('restart'); }
                    );
                }
            }, [icon('rotate-cw'), _('重启定位服务')])
        ]);

        document.body.classList.add('ils-location-page');
        root = E('div', { 'class': 'ils-app' }, [
            E('header', { 'class': 'ils-topbar' }, [
                E('div', { 'class': 'ils-title-group' }, [
                    E('span', { 'class': 'ils-title-mark' }, [icon('map-pinned')]),
                    E('h1', {}, _('iLS 定位管理'))
                ]),
                E('div', { 'class': 'ils-actions' }, [
                    button(_('安装证书'), 'badge-check', 'ils-button', openCertificate),
                    button(_('设置'), 'settings-2', 'ils-button', openSettings),
                    button(_('诊断'), 'activity', 'ils-button', openDiagnostics),
                    iconButton(_('更多操作'), 'ellipsis', function(event) {
                        event.stopPropagation();
                        menu.classList.toggle('is-open');
                    }, 'ils-icon-button')
                ])
            ]),
            menu,
            E('section', { 'class': 'ils-status-strip' + (effective ? ' is-enabled' : '') }, [
                E('div', { 'class': 'ils-status-main' }, [
                    E('span', { 'class': 'ils-status-dot' }),
                    E('div', { 'class': 'ils-status-copy' }, [
                        E('strong', {}, serviceTitle),
                        E('span', {}, serviceDetail)
                    ])
                ]),
                E('div', { 'class': 'ils-status-meta' }, [
                    E('span', {}, [icon('map-pin'), activeName]),
                    E('span', {}, [icon('smartphone'), _('%d 台授权设备').format(enabledDevices)]),
                    serviceSwitch
                ])
            ]),
            E('main', { 'class': 'ils-workspace' }, [
                E('section', { 'class': 'ils-pane' }, [
                    E('div', { 'class': 'ils-section-head' }, [
                        E('div', { 'class': 'ils-section-title' }, [E('h2', {}, _('定位点')), E('span', { 'class': 'ils-count' }, _('%d 个').format(profiles.length))]),
                        button(_('添加'), 'plus', 'ils-button ils-button-primary', function() { openProfileEditor(null); })
                    ]),
                    profileList
                ]),
                E('section', { 'class': 'ils-pane' }, [
                    E('div', { 'class': 'ils-section-head' }, [
                        E('div', { 'class': 'ils-section-title' }, [E('h2', {}, _('授权设备')), E('span', { 'class': 'ils-count' }, _('%d 台').format(devices.length))]),
                        button(_('添加'), 'plus', 'ils-button ils-button-primary', function() { openDeviceEditor(null); })
                    ]),
                    deviceList
                ])
            ])
        ]);

        root.addEventListener('click', function(event) {
            if (!menu.contains(event.target) && !event.target.closest('.ils-icon-button'))
                menu.classList.remove('is-open');
        });
        renderIcons();
        return root;
    }
});
