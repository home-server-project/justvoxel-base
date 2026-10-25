package org.homeserverproject.justvoxel.lobby;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

record LobbyConfig(String welcome, Map<String, String> servers) {
    private static final Pattern ID = Pattern.compile("jv-[a-z0-9]{6,12}");

    static LobbyConfig read(Path path) throws IOException {
        if (Files.size(path) > 32768) {
            throw new IOException("Lobby configuration is too large");
        }
        return parse(Files.readAllLines(path, StandardCharsets.UTF_8));
    }

    static LobbyConfig parse(List<String> lines) throws IOException {
        if (lines.size() < 2 || lines.size() > 29 || !lines.getFirst().equals("schema=1")
                || !lines.get(1).startsWith("welcome=")) {
            throw new IOException("Invalid lobby schema");
        }
        String welcome = decode(lines.get(1).substring(8), 512);
        Map<String, String> servers = new LinkedHashMap<>();
        for (String line : lines.subList(2, lines.size())) {
            if (!line.startsWith("server=")) {
                throw new IOException("Invalid lobby field");
            }
            int separator = line.indexOf(':', 7);
            if (separator < 0) {
                throw new IOException("Invalid member");
            }
            String id = line.substring(7, separator);
            String name = decode(line.substring(separator + 1), 512);
            if (!ID.matcher(id).matches() || name.isBlank() || servers.putIfAbsent(id, name) != null) {
                throw new IOException("Invalid member identity");
            }
        }
        return new LobbyConfig(welcome, Collections.unmodifiableMap(servers));
    }

    private static String decode(String encoded, int maximumBytes) throws IOException {
        try {
            byte[] bytes = Base64.getDecoder().decode(encoded);
            if (bytes.length > maximumBytes || !Base64.getEncoder().encodeToString(bytes).equals(encoded)) {
                throw new IOException("Invalid text encoding");
            }
            String text = StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(bytes)).toString();
            if (text.codePoints().anyMatch(c -> Character.isISOControl(c) || c == 0x2028 || c == 0x2029)) {
                throw new IOException("Invalid text character");
            }
            return text;
        } catch (IllegalArgumentException e) {
            throw new IOException("Invalid text encoding", e);
        }
    }
}
