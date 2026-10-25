package org.homeserverproject.justvoxel.lobby;

import static org.junit.jupiter.api.Assertions.*;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.List;
import org.junit.jupiter.api.Test;

final class LobbyConfigTest {
    private static String encoded(String text) {
        return Base64.getEncoder().encodeToString(text.getBytes(StandardCharsets.UTF_8));
    }

    @Test
    void presentationRoundTripsWithoutBecomingConfigurationOrRoutes() throws IOException {
        String welcome = "Welcome = : \\" + " <friends> ☃";
        String name = "Family's : = \\" + " \"Survival\" — 世界";
        var config = LobbyConfig.parse(List.of("schema=1", "welcome=" + encoded(welcome),
                "server=jv-a1b2c3:" + encoded(name)));
        assertEquals(welcome, config.welcome());
        assertEquals(name, config.servers().get("jv-a1b2c3"));
        assertFalse(config.servers().containsKey(name));
        assertFalse(config.servers().containsKey("jv-other1"));
        assertFalse(config.servers().containsKey("jv-lobby"));
        assertThrows(UnsupportedOperationException.class, () -> config.servers().put("other", "Other"));
    }

    @Test
    void emptyNetworkIsValid() throws IOException {
        assertTrue(LobbyConfig.parse(List.of("schema=1", "welcome=")).servers().isEmpty());
    }

    @Test
    void rejectsUnknownFieldsIdentitiesDuplicateMembersAndControlInjection() {
        for (String field : List.of("server=Friendly Name:" + encoded("Name"),
                "server=jv-a1b2c3:" + encoded("Name\nserver=evil"),
                "server=jv-a1b2c3:" + encoded("Name\r\n"),
                "server=jv-a1b2c3:" + encoded("Name\u0000"),
                "server=jv-a1b2c3:" + encoded("Name\u2028"),
                "server=jv-a1b2c3:***", "secret=forbidden", "schema=2")) {
            assertThrows(IOException.class, () -> LobbyConfig.parse(List.of("schema=1", "welcome=", field)));
        }
        assertThrows(IOException.class, () -> LobbyConfig.parse(List.of("schema=1", "welcome=",
                "server=jv-a1b2c3:QQ==", "server=jv-a1b2c3:Qg==")));
        assertThrows(IOException.class, () -> LobbyConfig.parse(List.of("schema=1", "welcome=/w==")));
        assertThrows(IOException.class, () -> LobbyConfig.parse(List.of("schema=1", "welcome=" + encoded("bad\nmessage"))));
    }
}
