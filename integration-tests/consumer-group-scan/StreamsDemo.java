import org.apache.kafka.common.serialization.Serdes;
import org.apache.kafka.streams.KafkaStreams;
import org.apache.kafka.streams.StreamsBuilder;
import org.apache.kafka.streams.StreamsConfig;
import org.apache.kafka.streams.kstream.KStream;

import java.util.Properties;

// Minimal Kafka Streams app that opts into the KIP-1071 streams group protocol
// (group.protocol=streams). It forms a real "streams"-type group on the broker,
// which the consumer-group scan then discovers. Used by setup.sh in the
// consumer-group-scan integration suite (compiled against the broker's own libs).
// Args: <bootstrap> [application-id]
public class StreamsDemo {
    public static void main(String[] args) throws Exception {
        String bootstrap = args.length > 0 ? args[0] : "localhost:39096";
        String appId = args.length > 1 ? args[1] : "streams-grp";

        Properties props = new Properties();
        props.put(StreamsConfig.APPLICATION_ID_CONFIG, appId);
        props.put(StreamsConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrap);
        props.put(StreamsConfig.DEFAULT_KEY_SERDE_CLASS_CONFIG, Serdes.String().getClass());
        props.put(StreamsConfig.DEFAULT_VALUE_SERDE_CLASS_CONFIG, Serdes.String().getClass());
        props.put(StreamsConfig.REPLICATION_FACTOR_CONFIG, 1);
        // KIP-1071: opt into the broker-coordinated streams group protocol.
        props.put("group.protocol", "streams");

        StreamsBuilder builder = new StreamsBuilder();
        KStream<String, String> src = builder.stream("orders");
        src.mapValues(v -> v == null ? "" : v.toUpperCase()).to("orders-streamed");

        KafkaStreams streams = new KafkaStreams(builder.build(), props);
        Runtime.getRuntime().addShutdownHook(new Thread(streams::close));
        streams.start();
        System.out.println("StreamsDemo running (group.protocol=streams, app=" + appId + ") against " + bootstrap);
        Thread.currentThread().join();
    }
}
