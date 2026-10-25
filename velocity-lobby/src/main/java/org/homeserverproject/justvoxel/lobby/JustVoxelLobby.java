package org.homeserverproject.justvoxel.lobby;

import com.google.inject.Inject;
import com.velocitypowered.api.command.SimpleCommand;
import com.velocitypowered.api.event.Subscribe;
import com.velocitypowered.api.event.EventTask;
import com.velocitypowered.api.event.PostOrder;
import com.velocitypowered.api.event.connection.DisconnectEvent;
import com.velocitypowered.api.event.connection.PluginMessageEvent;
import com.velocitypowered.api.event.player.PlayerChooseInitialServerEvent;
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent;
import com.velocitypowered.api.plugin.Dependency;
import com.velocitypowered.api.plugin.Plugin;
import com.velocitypowered.api.plugin.annotation.DataDirectory;
import com.velocitypowered.api.proxy.Player;
import com.velocitypowered.api.proxy.ProxyServer;
import java.io.IOException;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import com.velocitypowered.api.proxy.messages.MinecraftChannelIdentifier;
import com.velocitypowered.proxy.protocol.packet.PluginMessagePacket;
import io.netty.buffer.ByteBufUtil;
import net.elytrium.limboapi.api.Limbo;
import net.elytrium.limboapi.api.LimboFactory;
import net.elytrium.limboapi.api.LimboSessionHandler;
import net.elytrium.limboapi.api.chunk.Dimension;
import net.elytrium.limboapi.api.event.LoginLimboRegisterEvent;
import net.elytrium.limboapi.api.player.GameMode;
import net.elytrium.limboapi.api.player.LimboPlayer;
import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.event.ClickEvent;
import net.kyori.adventure.text.event.HoverEvent;
import net.kyori.adventure.text.format.NamedTextColor;
import org.geysermc.cumulus.form.SimpleForm;
import org.geysermc.floodgate.api.FloodgateApi;
import org.slf4j.Logger;

@Plugin(id = "justvoxel-lobby", name = "JustVoxel Lobby", version = "1.0.0",
        dependencies = {@Dependency(id = "limboapi"), @Dependency(id = "floodgate"),
                @Dependency(id = "global-whitelist")})
public final class JustVoxelLobby {
    private static final String UNAVAILABLE = "Server is currently unavailable.";
    private static final String RETRY = "Server selector is temporarily unavailable. Use /servers to try again.";
    private final ProxyServer proxy;
    private final Logger logger;
    private final Path dataDirectory;
    private final Map<UUID, Session> sessions = new ConcurrentHashMap<>();
    // Hold Velocity's automatic initial-connect continuation for the lifetime
    // of the connection: LimboAPI owns the explicit selected-server transfer.
    private final Map<Player, Runnable> initialConnections = new ConcurrentHashMap<>();
    private LimboFactory factory;
    private LobbyConfig config;
    private Limbo lobby;

    @Inject
    public JustVoxelLobby(ProxyServer proxy, Logger logger, @DataDirectory Path dataDirectory) {
        this.proxy = proxy;
        this.logger = logger;
        this.dataDirectory = dataDirectory;
    }

    @Subscribe
    public void initialize(ProxyInitializeEvent event) throws IOException {
        config = LobbyConfig.read(dataDirectory.resolve("lobby.conf"));
        factory = (LimboFactory) proxy.getPluginManager().getPlugin("limboapi")
                .flatMap(plugin -> plugin.getInstance()).orElseThrow();
        var world = factory.createVirtualWorld(Dimension.OVERWORLD, 0.5, 65, 0.5, 0, 0);
        // A tiny stationary platform; block encoding belongs entirely to LimboAPI.
        var block = factory.createSimpleBlock("minecraft:stone");
        for (int x = -2; x <= 2; x++) {
            for (int z = -2; z <= 2; z++) {
                world.setBlock(x, 64, z, block);
            }
        }
        lobby = factory.createLimbo(world).setName("JustVoxel Lobby")
                .setGameMode(GameMode.ADVENTURE).setViewDistance(2).setSimulationDistance(2);
        SimpleCommand command = invocation -> {
            if (invocation.source() instanceof Player player) {
                Session session = sessions.get(player.getUniqueId());
                if (session != null) {
                    session.command(invocation.arguments());
                } else if (invocation.arguments().length == 0 && player.isActive()) {
                    lobby.spawnPlayer(player, new Session(player));
                } else {
                    player.sendMessage(Component.text(UNAVAILABLE));
                }
            }
        };
        var meta = proxy.getCommandManager().metaBuilder("servers").plugin(this).build();
        // Limbo advertises the command; onChat handles it in the virtual session.
        lobby.registerCommand(meta, command);
        proxy.getCommandManager().register(meta, command);
    }

    @Subscribe
    public void registerLogin(LoginLimboRegisterEvent event) {
        // Complete the ordinary admission sequence first: Global Whitelist's
        // LoginEvent must run and Floodgate must see a registered proxy player
        // before the lobby can show a form. Never synthesize login events.
        event.addOnJoinCallback(() -> {
            if (factory == null || lobby == null) {
                event.getPlayer().disconnect(Component.text("Minecraft Network is temporarily unavailable."));
                return;
            }
            factory.passLoginLimbo(event.getPlayer());
        });
    }

    @Subscribe(order = PostOrder.FIRST)
    public EventTask initialServer(PlayerChooseInitialServerEvent event) {
        event.setInitialServer(null);
        return EventTask.withContinuation(continuation -> {
            Player player = event.getPlayer();
            initialConnections.put(player, continuation::resume);
            if (!player.isActive() || lobby == null) {
                player.disconnect(Component.text("Minecraft Network is temporarily unavailable."));
                resumeInitial(player);
                return;
            }
            lobby.spawnPlayer(player, new Session(player));
        });
    }

    @Subscribe
    public void disconnect(DisconnectEvent event) {
        resumeInitial(event.getPlayer());
    }

    private void resumeInitial(Player player) {
        Runnable resume = initialConnections.remove(player);
        if (resume != null) { resume.run(); }
    }

    private final class Session implements LimboSessionHandler {
        private final Player player;
        private LimboPlayer limboPlayer;
        private boolean transferring;

        private Session(Player player) { this.player = player; }

        @Override
        public void onSpawn(Limbo server, LimboPlayer spawned) {
            limboPlayer = spawned;
            spawned.disableFalling();
            sessions.put(player.getUniqueId(), this);
            selector();
        }

        @Override
        public void onChat(String message) {
            if (message.equalsIgnoreCase("/servers")) {
                selector();
            } else if (message.startsWith("/servers ")) {
                command(message.substring(9).split(" ", -1));
            }
        }

        @Override
        public void onGeneric(Object packet) {
            // LimboAPI supplies Velocity's already decoded packet. Forward only
            // native Floodgate form responses to Floodgate's normal event API;
            // there is no backend connection in the lobby to forward them to.
            // No protocol IDs, encoders or packet mappings live in this plugin.
            if (packet instanceof PluginMessagePacket message
                    && message.getChannel().equals("floodgate:form")) {
                try {
                    if (current() && FloodgateApi.getInstance().isFloodgatePlayer(player.getUniqueId())) {
                        var event = new PluginMessageEvent(player, player,
                                MinecraftChannelIdentifier.from("floodgate:form"),
                                ByteBufUtil.getBytes(message.content()));
                        event.setResult(PluginMessageEvent.ForwardResult.handled());
                        proxy.getEventManager().fireAndForget(event);
                    }
                } catch (RuntimeException | LinkageError error) {
                    player.sendMessage(Component.text(RETRY));
                }
            }
        }

        private void command(String[] arguments) {
            if (arguments.length == 0) {
                selector();
            } else if (arguments.length == 1) {
                select(arguments[0]);
            } else {
                player.sendMessage(Component.text(UNAVAILABLE));
            }
        }

        private boolean current() {
            return player.isActive() && sessions.get(player.getUniqueId()) == this && !transferring;
        }

        private void selector() {
            // Floodgate form callbacks and proxy commands may arrive on other
            // threads. Serialize all session work through LimboAPI's executor.
            limboPlayer.getScheduledExecutor().execute(() -> {
                if (!current()) { return; }
                if (config.servers().isEmpty()) {
                    player.sendMessage(Component.text(config.welcome()));
                    player.sendMessage(Component.text("No servers are currently available."));
                    return;
                }
                try {
                    FloodgateApi floodgate = FloodgateApi.getInstance();
                    if (floodgate.isFloodgatePlayer(player.getUniqueId())) {
                        var form = SimpleForm.builder().title("JustVoxel Servers").content(config.welcome());
                        List<String> serverIds = new ArrayList<>();
                        config.servers().forEach((id, name) -> {
                            serverIds.add(id);
                            form.button(name);
                        });
                        form.validResultHandler(response -> {
                            int index = response.clickedButtonId();
                            if (index >= 0 && index < serverIds.size()) {
                                select(serverIds.get(index));
                            }
                        });
                        if (!floodgate.sendForm(player.getUniqueId(), form.build())) {
                            player.sendMessage(Component.text(RETRY));
                        }
                        return;
                    }
                } catch (RuntimeException | LinkageError error) {
                    logger.warn("Minecraft Network selector delivery is temporarily unavailable.");
                    player.sendMessage(Component.text(RETRY));
                    return;
                }
                player.sendMessage(Component.text(config.welcome()));
                config.servers().forEach((id, name) -> player.sendMessage(
                        Component.text("• " + name, NamedTextColor.AQUA)
                                .hoverEvent(HoverEvent.showText(Component.text("Join " + name)))
                                .clickEvent(ClickEvent.runCommand("/servers " + id))));
                player.sendMessage(Component.text("Use /servers to show the server selector again."));
            });
        }

        private void select(String id) {
            limboPlayer.getScheduledExecutor().execute(() -> {
                if (!current()) { return; }
                // Friendly names and arbitrary registered servers are never routes.
                if (!config.servers().containsKey(id)) {
                    player.sendMessage(Component.text(UNAVAILABLE));
                    return;
                }
                var destination = proxy.getServer(id);
                if (destination.isEmpty()) {
                    player.sendMessage(Component.text(UNAVAILABLE));
                    return;
                }
                transferring = true;
                limboPlayer.disconnect(destination.get());
            });
        }

        @Override
        public void onDisconnect() {
            sessions.remove(player.getUniqueId(), this);
        }
    }
}
