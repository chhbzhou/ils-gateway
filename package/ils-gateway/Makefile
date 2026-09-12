include $(TOPDIR)/rules.mk

PKG_NAME:=ils-gateway
PKG_RELEASE:=43
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)
PKG_BUILD_DEPENDS:=golang/host luci-base/host

GO_PKG:=github.com/chhbzhou/ils-gateway
GO_PKG_BUILD_PKG:=github.com/chhbzhou/ils-gateway/cmd/locspoofd github.com/chhbzhou/ils-gateway/cmd/locspoof-ca github.com/chhbzhou/ils-gateway/cmd/locspoof-health github.com/chhbzhou/ils-gateway/cmd/locspoof-control github.com/chhbzhou/ils-gateway/cmd/locspoof-elevation github.com/chhbzhou/ils-gateway/cmd/locspoof-timeout

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/ils-gateway
  SECTION:=luci
  CATEGORY:=LuCI
  TITLE:=iLS Gateway OpenWrt integration
  USERID:=locspoofd:locspoofd
  PROVIDES:=luci-app-ios-location-spoofer
  CONFLICTS:=luci-app-ios-location-spoofer
  DEPENDS:=$(GO_ARCH_DEPENDS) +firewall4 +nftables-json +jsonfilter +openssl-util +rpcd +rpcd-mod-ucode \
	+coreutils-stat +shadow-su +ca-bundle +luci-base
endef

define Package/ils-gateway/description
  OpenWrt integration and Go data plane binaries for the iLS Gateway.
endef

define Package/ils-gateway/conffiles
/etc/config/ios-location-spoofer
endef

define Build/Prepare
	$(INSTALL_DIR) $(PKG_BUILD_DIR)
	$(CP) $(CURDIR)/../../cmd $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/../../internal $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/../../go.mod $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/../../LICENSE $(PKG_BUILD_DIR)/
endef

define Build/Compile
	$(call GoPackage/Build/Compile)
	po2lmo ./po/en/ils-gateway.po $(PKG_BUILD_DIR)/ils-gateway.en.lmo
endef

define Package/ils-gateway/install
	$(INSTALL_DIR) $(1)/etc/config $(1)/etc/init.d $(1)/etc/uci-defaults
	$(INSTALL_DIR) $(1)/usr/share/nftables.d/table-post
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d $(1)/usr/share/luci/menu.d
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_DIR) $(1)/usr/share/licenses/ils-gateway
	$(INSTALL_DIR) $(1)/usr/lib/lua/luci/i18n
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/ios-location-spoofer
	$(INSTALL_CONF) ./root/etc/config/ios-location-spoofer $(1)/etc/config/ios-location-spoofer
	$(INSTALL_BIN) ./root/etc/init.d/ios-location-spoofer $(1)/etc/init.d/
	$(INSTALL_DATA) ./root/etc/nftables.d/*.nft $(1)/usr/share/nftables.d/table-post/
	$(INSTALL_BIN) ./root/etc/uci-defaults/90-ios-location-spoofer $(1)/etc/uci-defaults/
	$(INSTALL_DATA) ./root/usr/share/rpcd/acl.d/* $(1)/usr/share/rpcd/acl.d/
	$(INSTALL_DATA) ./root/usr/share/luci/menu.d/* $(1)/usr/share/luci/menu.d/
	$(INSTALL_DATA) ./root/www/luci-static/resources/view/ios-location-spoofer/* $(1)/www/luci-static/resources/view/ios-location-spoofer/
	$(INSTALL_DATA) $(CURDIR)/../../LICENSE $(1)/usr/share/licenses/ils-gateway/LICENSE
	$(INSTALL_DATA) $(CURDIR)/../../THIRD_PARTY_NOTICES.md $(1)/usr/share/licenses/ils-gateway/THIRD_PARTY_NOTICES.md
	$(INSTALL_DATA) $(CURDIR)/../../third_party/qrcodejs/LICENSE $(1)/usr/share/licenses/ils-gateway/LICENSE.qrcodejs
	$(INSTALL_DATA) $(CURDIR)/../../third_party/lucide/LICENSE $(1)/usr/share/licenses/ils-gateway/LICENSE.lucide
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/ils-gateway.en.lmo $(1)/usr/lib/lua/luci/i18n/ils-gateway.en.lmo
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_BIN) ./root/usr/bin/locspoofctl $(1)/usr/bin/locspoofctl
	$(INSTALL_BIN) ./root/usr/bin/locspoof-watchdog $(1)/usr/bin/locspoof-watchdog
	$(INSTALL_BIN) ./root/usr/bin/locspoof-service $(1)/usr/bin/locspoof-service
	$(INSTALL_BIN) ./root/usr/bin/locspoof-profile $(1)/usr/bin/locspoof-profile
	$(INSTALL_BIN) ./root/usr/bin/locspoof-status $(1)/usr/bin/locspoof-status
endef

$(eval $(call BuildPackage,ils-gateway))
